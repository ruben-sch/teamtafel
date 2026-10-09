package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
)

const (
	// KontoAufbewahrung: so lange bleibt ein Konto ohne Verknüpfung bestehen.
	KontoAufbewahrung = 30 * 24 * time.Hour
	// KontoHinweisVorher: so lange vor der Löschung geht der Hinweis raus.
	KontoHinweisVorher = 7 * 24 * time.Hour
)

// Mailer verschickt Text-Mails.
type Mailer interface {
	Senden(ctx context.Context, an, betreff, text string) error
}

// KontoFrist beschreibt einen Lauf der Löschfrist für Konten ohne Verknüpfung.
type KontoFrist struct {
	// Verknuepft sind alle Konten, die in irgendeinem Verein Trainer, Admin, Spieler,
	// Vertretung oder Anfragende sind.
	Verknuepft []string
	// Vollstaendig bestätigt, dass Verknuepft aus allen Vereinen gesammelt wurde.
	// Ohne diese Bestätigung bricht der Lauf ab, damit kein Ladefehler zu Löschungen führt.
	Vollstaendig bool
	// Geschuetzt sind Adressen, die nie gelöscht werden (Super-Admins).
	Geschuetzt []string
	Mailer     Mailer
	// Erlaubt beschränkt den Versand auf diese Adressen (Staging); leer heißt alle.
	// Ohne verschickten Hinweis wird nicht gelöscht.
	Erlaubt []string
}

// KontenAufraeumen setzt die Löschfrist für Konten ohne Verknüpfung um: Es merkt sich,
// seit wann ein Konto unverknüpft ist, schickt 7 Tage vor Ablauf der 30 Tage einen
// Hinweis und löscht das Konto, wenn die Frist abgelaufen ist und der Hinweis mindestens
// 7 Tage zurückliegt. Plattform-Admins und geschützte Adressen bleiben unberührt.
func (s *Store) KontenAufraeumen(ctx context.Context, f KontoFrist) error {
	if !f.Vollstaendig {
		return errors.New("konten aufräumen: verknüpfungen nicht vollständig geladen")
	}
	jetzt := s.Now()
	verknuepft := f.Verknuepft
	if verknuepft == nil {
		verknuepft = []string{}
	}
	geschuetzt := make([]string, 0, len(f.Geschuetzt))
	for _, e := range f.Geschuetzt {
		geschuetzt = append(geschuetzt, NormalisiereEmail(e))
	}
	const behalten = `(id = ANY($1::uuid[]) OR plattform_admin OR email = ANY($2))`

	if _, err := s.pool.Exec(ctx, `UPDATE konto SET unverknuepft_seit = NULL, hinweis_am = NULL
WHERE unverknuepft_seit IS NOT NULL AND `+behalten, verknuepft, geschuetzt); err != nil {
		return fmt.Errorf("verknüpfte konten: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE konto SET unverknuepft_seit = $3
WHERE unverknuepft_seit IS NULL AND NOT `+behalten, verknuepft, geschuetzt, jetzt); err != nil {
		return fmt.Errorf("unverknüpfte konten merken: %w", err)
	}
	if err := s.hinweisen(ctx, f, jetzt); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM konto
WHERE unverknuepft_seit <= $3 AND hinweis_am <= $4 AND NOT `+behalten,
		verknuepft, geschuetzt, jetzt.Add(-KontoAufbewahrung), jetzt.Add(-KontoHinweisVorher))
	if err != nil {
		return fmt.Errorf("konten löschen: %w", err)
	}
	if n := tag.RowsAffected(); n > 0 {
		slog.Info("konten ohne verknüpfung gelöscht", "anzahl", n)
	}
	return nil
}

// hinweisen schickt den Hinweis vor der Löschung. Jedes Konto wird in einer eigenen
// Transaktion gesperrt, damit mehrere Instanzen nicht doppelt schicken; scheitert der
// Versand, bleibt hinweis_am leer und der nächste Lauf versucht es erneut.
func (s *Store) hinweisen(ctx context.Context, f KontoFrist, jetzt time.Time) error {
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM konto WHERE unverknuepft_seit <= $1 AND hinweis_am IS NULL`,
		jetzt.Add(-(KontoAufbewahrung - KontoHinweisVorher)))
	if err != nil {
		return fmt.Errorf("hinweise laden: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("hinweise laden: %w", err)
	}
	loeschung := jetzt.Add(KontoHinweisVorher).In(berlin).Format("02.01.2006")
	for _, id := range ids {
		err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			var email string
			err := tx.QueryRow(ctx, `SELECT email FROM konto WHERE id = $1 AND hinweis_am IS NULL
FOR UPDATE SKIP LOCKED`, id).Scan(&email)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if len(f.Erlaubt) > 0 && !slices.ContainsFunc(f.Erlaubt, func(e string) bool { return strings.EqualFold(e, email) }) {
				slog.Info("löschhinweis unterdrückt, adresse nicht freigegeben", "konto", id)
				return nil
			}
			if err := f.Mailer.Senden(ctx, email, "Dein Teamtafel-Konto wird gelöscht", hinweisText(loeschung)); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE konto SET hinweis_am = $2 WHERE id = $1`, id, jetzt)
			return err
		})
		if err != nil {
			slog.Warn("löschhinweis", "konto", id, "err", err)
		}
	}
	return nil
}

func hinweisText(datum string) string {
	return "Hallo,\n\n" +
		"dein Teamtafel-Konto gehört seit über drei Wochen zu keiner Mannschaft mehr. " +
		"Ab dem " + datum + " wird es deshalb gelöscht.\n\n" +
		"Willst du es behalten, tritt über den Team-Link deiner Mannschaft bei. " +
		"Sonst musst du nichts tun.\n"
}

var berlin = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return time.UTC
	}
	return loc
}()
