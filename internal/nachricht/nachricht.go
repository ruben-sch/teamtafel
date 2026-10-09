// Package nachricht benachrichtigt Konten über die Job-Queue.
//
// Die Fachpakete wählen Empfänger und Inhalt in ihrer Transaktion und reihen je
// Konto einen Job ein; die Zustellung läuft später im Worker. Inhalte enthalten
// keine Gesundheitsdaten und nur Vornamen, weil sie auf dem Sperrbildschirm landen können.
package nachricht

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruben-sch/teamtafel/internal/job"
)

// Art ist die Job-Art einer Benachrichtigung.
const Art = "nachricht"

// Inhalt einer Benachrichtigung.
type Inhalt struct {
	Betreff string `json:"betreff"`
	Text    string `json:"text"`
	// Pfad auf der Vereins-Subdomain, z. B. /t/{id}.
	Pfad string `json:"pfad"`
}

type payload struct {
	KontoID string `json:"konto_id"`
	Verein  string `json:"verein"` // Slug, für den Link
	Inhalt
}

// An reiht für jedes Konto (ohne Dubletten) eine Benachrichtigung ein.
func An(ctx context.Context, tx pgx.Tx, vereinID string, konten []string, in Inhalt, ab time.Time) error {
	if len(konten) == 0 {
		return nil
	}
	var slug string
	if err := tx.QueryRow(ctx, `SELECT slug FROM verein WHERE id = $1`, vereinID).Scan(&slug); err != nil {
		return fmt.Errorf("verein laden: %w", err)
	}
	konten = slices.Clone(konten)
	slices.Sort(konten)
	for _, k := range slices.Compact(konten) {
		if err := job.Einreihen(ctx, tx, Art, payload{KontoID: k, Verein: slug, Inhalt: in}, ab); err != nil {
			return err
		}
	}
	return nil
}

// Team liefert alle Konten einer Mannschaft: Trainer, Spieler mit eigenem Login und Vertretungen.
func Team(ctx context.Context, tx pgx.Tx, mannschaftID, ausser string) ([]string, error) {
	return konten(ctx, tx, `
SELECT konto_id::text FROM trainer WHERE mannschaft_id = $1
UNION SELECT sp.konto_id::text FROM kader k JOIN spieler sp ON sp.id = k.spieler_id
	WHERE k.mannschaft_id = $1 AND sp.konto_id IS NOT NULL
UNION SELECT v.konto_id::text FROM kader k JOIN vertretung v ON v.spieler_id = k.spieler_id WHERE k.mannschaft_id = $1`,
		mannschaftID, ausser)
}

// Trainer liefert die Trainer einer Mannschaft.
func Trainer(ctx context.Context, tx pgx.Tx, mannschaftID, ausser string) ([]string, error) {
	return konten(ctx, tx, `SELECT konto_id::text FROM trainer WHERE mannschaft_id = $1`, mannschaftID, ausser)
}

// Spieler liefert das eigene Konto eines Spielers und die seiner Vertretungen.
func Spieler(ctx context.Context, tx pgx.Tx, spielerID, ausser string) ([]string, error) {
	return konten(ctx, tx, `
SELECT konto_id::text FROM spieler WHERE id = $1 AND konto_id IS NOT NULL
UNION SELECT konto_id::text FROM vertretung WHERE spieler_id = $1`, spielerID, ausser)
}

func konten(ctx context.Context, tx pgx.Tx, sql, id, ausser string) ([]string, error) {
	rows, err := tx.Query(ctx, sql, id)
	if err != nil {
		return nil, fmt.Errorf("empfänger laden: %w", err)
	}
	ks, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("empfänger laden: %w", err)
	}
	return slices.DeleteFunc(ks, func(k string) bool { return k == ausser }), nil
}

// Mailer verschickt eine Text-Mail.
type Mailer interface {
	Senden(ctx context.Context, an, betreff, text string) error
}

// Zustellung stellt Benachrichtigungen zu, vorerst per E-Mail.
type Zustellung struct {
	pool   *pgxpool.Pool
	mailer Mailer
	// Scheme und BaseHost bilden den Link: <Scheme>://<slug>.<BaseHost><Pfad>.
	Scheme, BaseHost string
	// Erlaubt beschränkt den Versand auf diese Adressen (Staging); leer heißt alle.
	Erlaubt []string
}

// NewZustellung erzeugt eine Zustellung.
func NewZustellung(pool *pgxpool.Pool, m Mailer, scheme, baseHost string, erlaubt []string) *Zustellung {
	return &Zustellung{pool: pool, mailer: m, Scheme: scheme, BaseHost: baseHost, Erlaubt: erlaubt}
}

// Zustellen ist der Job-Handler für Art.
func (z *Zustellung) Zustellen(ctx context.Context, raw []byte) error {
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return job.Endgueltig(err)
	}
	var email string
	err := z.pool.QueryRow(ctx, `SELECT email FROM konto WHERE id = $1`, p.KontoID).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return job.Endgueltig(fmt.Errorf("konto %s gibt es nicht mehr", p.KontoID))
	}
	if err != nil {
		return err
	}
	if len(z.Erlaubt) > 0 && !slices.ContainsFunc(z.Erlaubt, func(e string) bool { return strings.EqualFold(e, email) }) {
		slog.Info("benachrichtigung unterdrückt, adresse nicht freigegeben", "konto", p.KontoID)
		return nil
	}
	text := p.Text + "\n\n" + z.Scheme + "://" + p.Verein + "." + z.BaseHost + p.Pfad +
		"\n\n-- \nDu bekommst diese E-Mail, weil du in Teamtafel zu einer Mannschaft gehörst.\n"
	return z.mailer.Senden(ctx, email, p.Betreff, text)
}
