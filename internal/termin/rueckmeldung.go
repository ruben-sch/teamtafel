package termin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ruben-sch/teamtafel/internal/nachricht"
)

// Status einer Rückmeldung; leer heißt offen.
const (
	Zu = "zu"
	Ab = "ab"
)

// Grund einer Absage; bewusst kein Freitext.
const (
	GrundKrank     = "krank"
	GrundUrlaub    = "urlaub"
	GrundSonstiges = "sonstiges"
)

var (
	// ErrNichtBerechtigt: Das Konto darf für diesen Spieler nicht antworten.
	ErrNichtBerechtigt = errors.New("keine berechtigung für diesen spieler")
	// ErrFristVorbei: Nach der Frist (ohne Frist: nach Beginn) ändern nur noch Trainer.
	ErrFristVorbei = errors.New("rückmeldefrist vorbei")
	// ErrAbgesagt: Für abgesagte Termine gibt es keine Rückmeldung.
	ErrAbgesagt = errors.New("termin ist abgesagt")
)

// Rueckmeldung ist der Stand eines Spielers zu einem Termin.
type Rueckmeldung struct {
	SpielerID   string
	Vorname     string
	Nachname    string
	Status      string // Zu, Ab oder leer
	Grund       string
	GeaendertAm *time.Time
}

// darfAntworten: eigener Spieler oder vertretenes Kind, solange es minderjährig ist.
const darfAntworten = `(sp.konto_id = $KONTO OR (sp.jahrgang + 18 > $JAHR AND EXISTS (
	SELECT 1 FROM vertretung v WHERE v.spieler_id = sp.id AND v.konto_id = $KONTO)))`

// Rueckmelden setzt Zu- oder Absage. Trainer (alsTrainer) dürfen für jeden im Kader
// und auch nach der Frist; sonst gelten Vertretungsrecht und Frist.
func (s *Store) Rueckmelden(ctx context.Context, vereinID, terminID, spielerID, kontoID, status, grund string, alsTrainer bool) error {
	if (status != Zu && status != Ab) || (status == Zu && grund != "") ||
		(grund != "" && grund != GrundKrank && grund != GrundUrlaub && grund != GrundSonstiges) {
		return fmt.Errorf("%w: rückmeldung %q/%q", ErrUngueltig, status, grund)
	}
	jetzt := s.Now()
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		var abgesagt, imKader, berechtigt bool
		var ende time.Time
		var d Daten
		var mannschaftID, vorname, alt string
		err := tx.QueryRow(ctx, `
SELECT t.abgesagt, coalesce(t.frist, t.beginn), t.mannschaft_id::text, t.typ, t.titel, t.beginn,
	EXISTS (SELECT 1 FROM kader k WHERE k.mannschaft_id = t.mannschaft_id AND k.spieler_id = $2),
	EXISTS (SELECT 1 FROM spieler sp WHERE sp.id = $2 AND `+platzhalter(darfAntworten, "$3", "$4")+`),
	coalesce((SELECT vorname FROM spieler WHERE id = $2), ''),
	coalesce((SELECT status FROM rueckmeldung r WHERE r.termin_id = t.id AND r.spieler_id = $2), '')
FROM termin t WHERE t.id = $1`, terminID, spielerID, kontoID, jetzt.Year()).
			Scan(&abgesagt, &ende, &mannschaftID, &d.Typ, &d.Titel, &d.Beginn, &imKader, &berechtigt, &vorname, &alt)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return err
		case abgesagt:
			return ErrAbgesagt
		case !imKader || (!alsTrainer && !berechtigt):
			return ErrNichtBerechtigt
		case !alsTrainer && !jetzt.Before(ende):
			return ErrFristVorbei
		}
		var g *string
		if grund != "" {
			g = &grund
		}
		_, err = tx.Exec(ctx, `
INSERT INTO rueckmeldung (verein_id, termin_id, spieler_id, status, grund, von_konto_id, geaendert_am)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (termin_id, spieler_id) DO UPDATE
SET status = EXCLUDED.status, grund = EXCLUDED.grund, von_konto_id = EXCLUDED.von_konto_id, geaendert_am = EXCLUDED.geaendert_am`,
			vereinID, terminID, spielerID, status, g, kontoID, jetzt)
		if err != nil || status == alt {
			return err
		}
		return s.rueckmeldungMelden(ctx, tx, vereinID, mannschaftID, terminID, spielerID, kontoID, vorname, status, d,
			!jetzt.Before(ende))
	})
}

// rueckmeldungMelden informiert die anderen Konten des Spielers und bei einer Absage nach der Frist die Trainer.
// Der Grund bleibt draußen, er kann Gesundheitsdaten verraten.
func (s *Store) rueckmeldungMelden(ctx context.Context, tx pgx.Tx, vereinID, mannschaftID, terminID, spielerID, von, vorname,
	status string, d Daten, nachFrist bool) error {
	wort := "zugesagt"
	if status == Ab {
		wort = "abgesagt"
	}
	in := nachricht.Inhalt{
		Betreff: vorname + " hat " + wort + ": " + kurz(d),
		Text:    "Für " + vorname + " wurde " + kurz(d) + " " + wort + ".",
		Pfad:    "/t/" + terminID,
	}
	konten, err := nachricht.Spieler(ctx, tx, spielerID, von)
	if err != nil {
		return err
	}
	if status == Ab && nachFrist {
		trainer, err := nachricht.Trainer(ctx, tx, mannschaftID, von)
		if err != nil {
			return err
		}
		konten = append(konten, trainer...)
		in.Betreff = "Absage nach Frist: " + vorname + ", " + kurz(d)
	}
	return nachricht.An(ctx, tx, vereinID, konten, in, s.Now())
}

// Rueckmeldungen listet alle Spieler im Kader der Termin-Mannschaft mit ihrem Stand.
func (s *Store) Rueckmeldungen(ctx context.Context, vereinID, terminID string) ([]Rueckmeldung, error) {
	var out []Rueckmeldung
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT sp.id::text, sp.vorname, sp.nachname, coalesce(r.status, ''), coalesce(r.grund, ''), r.geaendert_am
FROM termin t
JOIN kader k ON k.mannschaft_id = t.mannschaft_id
JOIN spieler sp ON sp.id = k.spieler_id
LEFT JOIN rueckmeldung r ON r.termin_id = t.id AND r.spieler_id = sp.id
WHERE t.id = $1
ORDER BY sp.vorname, sp.nachname`, terminID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Rueckmeldung, error) {
			var x Rueckmeldung
			return x, r.Scan(&x.SpielerID, &x.Vorname, &x.Nachname, &x.Status, &x.Grund, &x.GeaendertAm)
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("rückmeldungen laden: %w", err)
	}
	return out, nil
}

// MeineSpieler liefert je Termin die Spieler, für die das Konto antworten darf, mit ihrem Stand.
func (s *Store) MeineSpieler(ctx context.Context, vereinID, kontoID string, terminIDs []string) (map[string][]Rueckmeldung, error) {
	out := map[string][]Rueckmeldung{}
	if len(terminIDs) == 0 {
		return out, nil
	}
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT t.id::text, sp.id::text, sp.vorname, sp.nachname, coalesce(r.status, ''), coalesce(r.grund, ''), r.geaendert_am
FROM termin t
JOIN kader k ON k.mannschaft_id = t.mannschaft_id
JOIN spieler sp ON sp.id = k.spieler_id
LEFT JOIN rueckmeldung r ON r.termin_id = t.id AND r.spieler_id = sp.id
WHERE t.id = ANY($1::uuid[]) AND `+platzhalter(darfAntworten, "$2", "$3")+`
ORDER BY sp.vorname`, terminIDs, kontoID, s.Now().Year())
		if err != nil {
			return err
		}
		var terminID string
		var x Rueckmeldung
		_, err = pgx.ForEachRow(rows, []any{&terminID, &x.SpielerID, &x.Vorname, &x.Nachname, &x.Status, &x.Grund, &x.GeaendertAm},
			func() error {
				out[terminID] = append(out[terminID], x)
				return nil
			})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("eigene spieler laden: %w", err)
	}
	return out, nil
}

// platzhalter setzt die Parameternummern für Konto und Jahr in darfAntworten ein.
func platzhalter(sql, konto, jahr string) string {
	return strings.NewReplacer("$KONTO", konto+"::uuid", "$JAHR", jahr+"::int").Replace(sql)
}
