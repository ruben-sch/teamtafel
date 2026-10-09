// Package termin verwaltet Termine und wöchentliche Terminserien.
package termin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	// Zeitzonendaten im Binary; hier statt in main, weil Zeitzone schon beim Init geladen wird.
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruben-sch/teamtafel/internal/db"
)

// Typ eines Termins.
const (
	TypTraining  = "training"
	TypSpiel     = "spiel"
	TypSonstiges = "sonstiges"
)

// Vorlauf: so weit im Voraus erzeugt eine Serie ihre Termine.
const Vorlauf = 8 * 7 * 24 * time.Hour

var (
	// ErrNotFound: kein Termin bzw. keine Serie mit dieser ID.
	ErrNotFound = errors.New("termin nicht gefunden")
	// ErrUngueltig: Angaben unvollständig oder widersprüchlich.
	ErrUngueltig = errors.New("termin ungültig")
)

// Zeitzone ist die Zeitzone aller Termine; gespeichert wird in UTC.
var Zeitzone = mustLoad("Europe/Berlin")

func mustLoad(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

// Daten sind die änderbaren Angaben eines Termins.
type Daten struct {
	Typ        string
	Titel      string
	Beginn     time.Time
	Ende       time.Time
	Treffzeit  *time.Time
	Frist      *time.Time
	Ort        string
	Treffpunkt string
}

func (d *Daten) pruefen() error {
	d.Titel, d.Ort, d.Treffpunkt = strings.TrimSpace(d.Titel), strings.TrimSpace(d.Ort), strings.TrimSpace(d.Treffpunkt)
	switch {
	case d.Typ != TypTraining && d.Typ != TypSpiel && d.Typ != TypSonstiges:
		return fmt.Errorf("%w: typ %q", ErrUngueltig, d.Typ)
	case d.Beginn.IsZero() || !d.Ende.After(d.Beginn):
		return fmt.Errorf("%w: ende muss nach beginn liegen", ErrUngueltig)
	case d.Treffzeit != nil && d.Treffzeit.After(d.Beginn):
		return fmt.Errorf("%w: treffzeit nach beginn", ErrUngueltig)
	case d.Frist != nil && d.Frist.After(d.Beginn):
		return fmt.Errorf("%w: frist nach beginn", ErrUngueltig)
	}
	return nil
}

// Termin ist ein konkreter Termin einer Mannschaft.
type Termin struct {
	ID           string
	MannschaftID string
	Mannschaft   string
	SerieID      string
	Daten
	Abgesagt   bool
	Bearbeitet bool
	// Zähler über den Kader der Mannschaft.
	Zu, Ab, Offen int
}

// SerieDaten beschreiben ein wöchentliches Training.
type SerieDaten struct {
	Wochentag time.Weekday
	// Uhrzeit des Beginns in Europe/Berlin, "HH:MM".
	Uhrzeit     string
	Dauer       time.Duration
	TreffVorher time.Duration // 0: keine Treffzeit
	FristVorher time.Duration // 0: keine Rückmeldefrist
	Ort         string
	// GueltigVon und GueltigBis sind Kalendertage, die Uhrzeit wird ignoriert.
	GueltigVon time.Time
	GueltigBis *time.Time
}

// Serie ist eine aktive Terminserie.
type Serie struct {
	ID           string
	MannschaftID string
	SerieDaten
}

func (d *SerieDaten) pruefen() (stunde, minute int, err error) {
	d.Ort = strings.TrimSpace(d.Ort)
	t, perr := time.Parse("15:04", d.Uhrzeit)
	switch {
	case perr != nil:
		return 0, 0, fmt.Errorf("%w: uhrzeit %q", ErrUngueltig, d.Uhrzeit)
	case d.Wochentag < time.Sunday || d.Wochentag > time.Saturday:
		return 0, 0, fmt.Errorf("%w: wochentag", ErrUngueltig)
	case d.Dauer <= 0 || d.TreffVorher < 0 || d.FristVorher < 0:
		return 0, 0, fmt.Errorf("%w: dauer", ErrUngueltig)
	case d.GueltigVon.IsZero() || (d.GueltigBis != nil && tag(*d.GueltigBis).Before(tag(d.GueltigVon))):
		return 0, 0, fmt.Errorf("%w: gültigkeit", ErrUngueltig)
	}
	return t.Hour(), t.Minute(), nil
}

// tag liefert den Kalendertag als Mitternacht UTC, passend zu SQL date.
func tag(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }

// isoWochentag: 1 = Montag bis 7 = Sonntag.
func isoWochentag(w time.Weekday) int {
	if w == time.Sunday {
		return 7
	}
	return int(w)
}

// Store greift mit der App-Rolle auf die Datenbank zu; alles läuft in InVerein.
type Store struct {
	pool *pgxpool.Pool
	Now  func() time.Time
}

// NewStore erzeugt einen Store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, Now: time.Now}
}

func (s *Store) inVerein(ctx context.Context, vereinID string, fn func(pgx.Tx) error) error {
	return db.InVerein(ctx, s.pool, vereinID, fn)
}

// Anlegen legt einen Einzeltermin an.
func (s *Store) Anlegen(ctx context.Context, vereinID, mannschaftID string, d Daten) (Termin, error) {
	if err := d.pruefen(); err != nil {
		return Termin{}, err
	}
	t := Termin{MannschaftID: mannschaftID, Daten: d}
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
INSERT INTO termin (verein_id, mannschaft_id, typ, titel, beginn, ende, treffzeit, frist, ort, treffpunkt)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id::text`,
			vereinID, mannschaftID, d.Typ, d.Titel, d.Beginn, d.Ende, d.Treffzeit, d.Frist, d.Ort, d.Treffpunkt).Scan(&t.ID)
	})
	if err != nil {
		return Termin{}, fmt.Errorf("termin anlegen: %w", err)
	}
	return t, nil
}

// Aendern überschreibt die Angaben; Serientermine gelten danach als einzeln bearbeitet.
func (s *Store) Aendern(ctx context.Context, vereinID, terminID string, d Daten) error {
	if err := d.pruefen(); err != nil {
		return err
	}
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
UPDATE termin SET typ = $2, titel = $3, beginn = $4, ende = $5, treffzeit = $6, frist = $7, ort = $8, treffpunkt = $9,
	bearbeitet = true
WHERE id = $1`, terminID, d.Typ, d.Titel, d.Beginn, d.Ende, d.Treffzeit, d.Frist, d.Ort, d.Treffpunkt)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

// Absagen markiert den Termin als ausgefallen; er bleibt sichtbar.
func (s *Store) Absagen(ctx context.Context, vereinID, terminID string) error {
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE termin SET abgesagt = true, bearbeitet = true WHERE id = $1`, terminID)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

const terminSpalten = `t.id::text, t.mannschaft_id::text, m.name, coalesce(t.serie_id::text, ''), t.typ, t.titel,
	t.beginn, t.ende, t.treffzeit, t.frist, t.ort, t.treffpunkt, t.abgesagt, t.bearbeitet,
	(SELECT count(*) FILTER (WHERE r.status = 'zu') FROM rueckmeldung r JOIN kader k ON k.spieler_id = r.spieler_id
		AND k.mannschaft_id = t.mannschaft_id WHERE r.termin_id = t.id),
	(SELECT count(*) FILTER (WHERE r.status = 'ab') FROM rueckmeldung r JOIN kader k ON k.spieler_id = r.spieler_id
		AND k.mannschaft_id = t.mannschaft_id WHERE r.termin_id = t.id),
	(SELECT count(*) FROM kader k WHERE k.mannschaft_id = t.mannschaft_id)`

func scanTermin(r pgx.CollectableRow) (Termin, error) {
	var t Termin
	err := r.Scan(&t.ID, &t.MannschaftID, &t.Mannschaft, &t.SerieID, &t.Typ, &t.Titel,
		&t.Beginn, &t.Ende, &t.Treffzeit, &t.Frist, &t.Ort, &t.Treffpunkt, &t.Abgesagt, &t.Bearbeitet,
		&t.Zu, &t.Ab, &t.Offen)
	t.Offen -= t.Zu + t.Ab
	t.Beginn, t.Ende = t.Beginn.In(Zeitzone), t.Ende.In(Zeitzone)
	for _, p := range []*time.Time{t.Treffzeit, t.Frist} {
		if p != nil {
			*p = p.In(Zeitzone)
		}
	}
	return t, err
}

// Termin liefert einen Termin des Vereins.
func (s *Store) Termin(ctx context.Context, vereinID, terminID string) (Termin, error) {
	var t Termin
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+terminSpalten+`
FROM termin t JOIN mannschaft m ON m.id = t.mannschaft_id WHERE t.id = $1`, terminID)
		if err != nil {
			return err
		}
		t, err = pgx.CollectExactlyOneRow(rows, scanTermin)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// Kommende listet die Termine einer Mannschaft, die zwischen ab und bis beginnen.
func (s *Store) Kommende(ctx context.Context, vereinID, mannschaftID string, ab, bis time.Time) ([]Termin, error) {
	return s.liste(ctx, vereinID, `t.mannschaft_id = $1`, mannschaftID, ab, bis)
}

// teamVon: Mannschaften, in denen das Konto Trainer ist oder einen Spieler hat (selbst oder vertreten).
const teamVon = `(
	SELECT mannschaft_id FROM trainer WHERE konto_id = $1
	UNION
	SELECT k.mannschaft_id FROM kader k JOIN spieler sp ON sp.id = k.spieler_id
	WHERE sp.konto_id = $1 OR EXISTS (SELECT 1 FROM vertretung v WHERE v.spieler_id = sp.id AND v.konto_id = $1))`

// FuerKonto listet die Termine aller Mannschaften, zu denen das Konto gehört.
func (s *Store) FuerKonto(ctx context.Context, vereinID, kontoID string, ab, bis time.Time) ([]Termin, error) {
	return s.liste(ctx, vereinID, `t.mannschaft_id IN `+teamVon, kontoID, ab, bis)
}

// GehoertZumTeam prüft, ob das Konto Trainer der Mannschaft ist oder einen Spieler darin hat.
func (s *Store) GehoertZumTeam(ctx context.Context, vereinID, mannschaftID, kontoID string) (bool, error) {
	var ok bool
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT $2::uuid IN `+teamVon, kontoID, mannschaftID).Scan(&ok)
	})
	return ok, err
}

func (s *Store) liste(ctx context.Context, vereinID, filter, arg string, ab, bis time.Time) ([]Termin, error) {
	var out []Termin
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+terminSpalten+`
FROM termin t JOIN mannschaft m ON m.id = t.mannschaft_id
WHERE `+filter+` AND t.beginn >= $2 AND t.beginn < $3 ORDER BY t.beginn, m.name`, arg, ab, bis)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanTermin)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("termine laden: %w", err)
	}
	return out, nil
}

// SerieAnlegen legt eine Serie an und erzeugt ihre Termine für den Vorlauf.
func (s *Store) SerieAnlegen(ctx context.Context, vereinID, mannschaftID string, d SerieDaten) (Serie, error) {
	if _, _, err := d.pruefen(); err != nil {
		return Serie{}, err
	}
	sr := Serie{MannschaftID: mannschaftID, SerieDaten: d}
	var bis *time.Time
	if d.GueltigBis != nil {
		b := tag(*d.GueltigBis)
		bis = &b
	}
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
INSERT INTO terminserie (verein_id, mannschaft_id, wochentag, uhrzeit, dauer_min, treff_min, frist_min, ort, gueltig_von, gueltig_bis)
VALUES ($1, $2, $3, $4::time, $5, $6, $7, $8, $9, $10) RETURNING id::text`,
			vereinID, mannschaftID, isoWochentag(d.Wochentag), d.Uhrzeit, int(d.Dauer.Minutes()),
			int(d.TreffVorher.Minutes()), int(d.FristVorher.Minutes()), d.Ort, tag(d.GueltigVon), bis).Scan(&sr.ID)
		if err != nil {
			return err
		}
		return s.erzeugen(ctx, tx, vereinID, sr)
	})
	if err != nil {
		return Serie{}, fmt.Errorf("serie anlegen: %w", err)
	}
	return sr, nil
}

// erzeugen legt die fehlenden Termine der Serie von heute bis zum Ende des Vorlaufs an.
func (s *Store) erzeugen(ctx context.Context, tx pgx.Tx, vereinID string, sr Serie) error {
	stunde, minute, err := sr.pruefen()
	if err != nil {
		return err
	}
	jetzt := s.Now().In(Zeitzone)
	von, bis := tag(jetzt), tag(jetzt.Add(Vorlauf))
	if v := tag(sr.GueltigVon); v.After(von) {
		von = v
	}
	if sr.GueltigBis != nil && tag(*sr.GueltigBis).Before(bis) {
		bis = tag(*sr.GueltigBis)
	}
	for d := von; !d.After(bis); d = d.AddDate(0, 0, 1) {
		if d.Weekday() != sr.Wochentag {
			continue
		}
		beginn := time.Date(d.Year(), d.Month(), d.Day(), stunde, minute, 0, 0, Zeitzone)
		var treff, frist *time.Time
		if sr.TreffVorher > 0 {
			t := beginn.Add(-sr.TreffVorher)
			treff = &t
		}
		if sr.FristVorher > 0 {
			f := beginn.Add(-sr.FristVorher)
			frist = &f
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO termin (verein_id, mannschaft_id, serie_id, serie_datum, typ, beginn, ende, treffzeit, frist, ort)
VALUES ($1, $2, $3, $4, 'training', $5, $6, $7, $8, $9)
ON CONFLICT (serie_id, serie_datum) DO NOTHING`,
			vereinID, sr.MannschaftID, sr.ID, d, beginn, beginn.Add(sr.Dauer), treff, frist, sr.Ort); err != nil {
			return fmt.Errorf("serientermin: %w", err)
		}
	}
	return nil
}

// Serien listet die aktiven Serien einer Mannschaft.
func (s *Store) Serien(ctx context.Context, vereinID, mannschaftID string) ([]Serie, error) {
	return s.serien(ctx, vereinID, `mannschaft_id = $1::uuid`, mannschaftID)
}

func (s *Store) serien(ctx context.Context, vereinID, filter string, args ...any) ([]Serie, error) {
	var out []Serie
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT id::text, mannschaft_id::text, wochentag, to_char(uhrzeit, 'HH24:MI'), dauer_min, treff_min, frist_min, ort,
	gueltig_von, gueltig_bis
FROM terminserie WHERE beendet_am IS NULL AND `+filter+` ORDER BY wochentag, uhrzeit`, args...)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanSerie)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("serien laden: %w", err)
	}
	return out, nil
}

func scanSerie(r pgx.CollectableRow) (Serie, error) {
	var sr Serie
	var wt, dauer, treff, frist int
	err := r.Scan(&sr.ID, &sr.MannschaftID, &wt, &sr.Uhrzeit, &dauer, &treff, &frist, &sr.Ort, &sr.GueltigVon, &sr.GueltigBis)
	sr.Wochentag = time.Weekday(wt % 7)
	sr.Dauer, sr.TreffVorher, sr.FristVorher = time.Duration(dauer)*time.Minute,
		time.Duration(treff)*time.Minute, time.Duration(frist)*time.Minute
	return sr, err
}

// SerieBeenden stoppt die Serie und löscht ihre künftigen Termine, außer einzeln bearbeiteten
// und solchen mit Rückmeldungen; die sagt der Trainer bei Bedarf selbst ab.
func (s *Store) SerieBeenden(ctx context.Context, vereinID, serieID string) error {
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE terminserie SET beendet_am = $2 WHERE id = $1 AND beendet_am IS NULL`, serieID, s.Now())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `DELETE FROM termin t WHERE serie_id = $1 AND beginn > $2 AND NOT bearbeitet
			AND NOT EXISTS (SELECT 1 FROM rueckmeldung r WHERE r.termin_id = t.id)`, serieID, s.Now())
		return err
	})
}

// Fortschreiben ergänzt für alle aktiven Serien des Vereins die Termine bis zum Ende des Vorlaufs.
// Der Job ruft das regelmäßig auf; doppelte Aufrufe sind harmlos.
func (s *Store) Fortschreiben(ctx context.Context, vereinID string) error {
	serien, err := s.serien(ctx, vereinID, `true`)
	if err != nil {
		return err
	}
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		for _, sr := range serien {
			if err := s.erzeugen(ctx, tx, vereinID, sr); err != nil {
				return err
			}
		}
		return nil
	})
}

// Serie liefert eine aktive Serie des Vereins.
func (s *Store) Serie(ctx context.Context, vereinID, serieID string) (Serie, error) {
	serien, err := s.serien(ctx, vereinID, `id = $1::uuid`, serieID)
	if err != nil {
		return Serie{}, err
	}
	if len(serien) == 0 {
		return Serie{}, ErrNotFound
	}
	return serien[0], nil
}
