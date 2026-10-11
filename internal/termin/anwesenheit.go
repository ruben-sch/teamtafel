package termin

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrNichtBegonnen: Anwesenheit gibt es erst ab Beginn des Termins.
var ErrNichtBegonnen = errors.New("termin hat noch nicht begonnen")

// anwesendSQL: Korrektur des Trainers, sonst die Zusage; braucht die Aliase r (rueckmeldung) und a (anwesenheit).
const anwesendSQL = `coalesce(a.anwesend, r.status = 'zu', false)`

// Beteiligung zählt die vergangenen, nicht abgesagten Termine und wie oft der Spieler da war.
type Beteiligung struct {
	Termine, Anwesend int
}

// Quote ist der Anteil der Anwesenheit in Prozent, gerundet; ohne Termine 0.
func (b Beteiligung) Quote() int {
	if b.Termine == 0 {
		return 0
	}
	return (200*b.Anwesend + b.Termine) / (2 * b.Termine)
}

func (b *Beteiligung) add(o Beteiligung) {
	b.Termine, b.Anwesend = b.Termine+o.Termine, b.Anwesend+o.Anwesend
}

// Anwesenheit ist die Beteiligung eines Spielers im Kader. Absagegründe fließen bewusst nicht ein.
type Anwesenheit struct {
	SpielerID string
	Vorname   string
	Nachname  string
	Training  Beteiligung
	Spiel     Beteiligung
	// Gesamt umfasst auch sonstige Termine.
	Gesamt Beteiligung
}

// AnwesenheitSetzen hält fest, ob ein Spieler da war, unabhängig von seiner Rückmeldung.
// Ohne Benachrichtigung; die Berechtigung (Trainer) prüft der Aufrufer.
func (s *Store) AnwesenheitSetzen(ctx context.Context, vereinID, terminID, spielerID, kontoID string, anwesend bool) error {
	jetzt := s.Now()
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		var abgesagt, begonnen, imKader bool
		err := tx.QueryRow(ctx, `
SELECT t.abgesagt, t.beginn <= $3,
	EXISTS (SELECT 1 FROM kader k WHERE k.mannschaft_id = t.mannschaft_id AND k.spieler_id = $2)
FROM termin t WHERE t.id = $1`, terminID, spielerID, jetzt).Scan(&abgesagt, &begonnen, &imKader)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return err
		case abgesagt:
			return ErrAbgesagt
		case !begonnen:
			return ErrNichtBegonnen
		case !imKader:
			return ErrNichtBerechtigt
		}
		_, err = tx.Exec(ctx, `
INSERT INTO anwesenheit (verein_id, termin_id, spieler_id, anwesend, von_konto_id, geaendert_am)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (termin_id, spieler_id) DO UPDATE
SET anwesend = EXCLUDED.anwesend, von_konto_id = EXCLUDED.von_konto_id, geaendert_am = EXCLUDED.geaendert_am`,
			vereinID, terminID, spielerID, anwesend, kontoID, jetzt)
		return err
	})
}

// Anwesenheit liefert je Spieler im Kader die Anwesenheit bei den Terminen der Mannschaft,
// die bereits begonnen haben. Eine Mannschaft gehört zu einer Saison, also gilt die Statistik je Saison.
func (s *Store) Anwesenheit(ctx context.Context, vereinID, mannschaftID string) ([]Anwesenheit, error) {
	var out []Anwesenheit
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT sp.id::text, sp.vorname, sp.nachname, coalesce(t.typ, ''), count(t.id)::int,
	(count(t.id) FILTER (WHERE `+anwesendSQL+`))::int
FROM kader k
JOIN spieler sp ON sp.id = k.spieler_id
LEFT JOIN termin t ON t.mannschaft_id = k.mannschaft_id AND NOT t.abgesagt AND t.beginn <= $2
LEFT JOIN rueckmeldung r ON r.termin_id = t.id AND r.spieler_id = sp.id
LEFT JOIN anwesenheit a ON a.termin_id = t.id AND a.spieler_id = sp.id
WHERE k.mannschaft_id = $1
GROUP BY sp.id, sp.vorname, sp.nachname, t.typ
ORDER BY sp.nachname, sp.vorname, sp.id`, mannschaftID, s.Now())
		if err != nil {
			return err
		}
		var id, vorname, nachname, typ string
		var b Beteiligung
		_, err = pgx.ForEachRow(rows, []any{&id, &vorname, &nachname, &typ, &b.Termine, &b.Anwesend}, func() error {
			if len(out) == 0 || out[len(out)-1].SpielerID != id {
				out = append(out, Anwesenheit{SpielerID: id, Vorname: vorname, Nachname: nachname})
			}
			a := &out[len(out)-1]
			switch typ {
			case TypTraining:
				a.Training.add(b)
			case TypSpiel:
				a.Spiel.add(b)
			}
			a.Gesamt.add(b)
			return nil
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("anwesenheit laden: %w", err)
	}
	return out, nil
}
