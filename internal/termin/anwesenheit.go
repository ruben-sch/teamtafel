package termin

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Beteiligung zählt die vergangenen, nicht abgesagten Termine und die Rückmeldungen dazu.
type Beteiligung struct {
	Termine, Zu, Ab int
}

// Offen: Termine ohne Rückmeldung.
func (b Beteiligung) Offen() int { return b.Termine - b.Zu - b.Ab }

// Quote ist der Anteil der Zusagen in Prozent, gerundet; ohne Termine 0.
func (b Beteiligung) Quote() int {
	if b.Termine == 0 {
		return 0
	}
	return (200*b.Zu + b.Termine) / (2 * b.Termine)
}

func (b *Beteiligung) add(o Beteiligung) {
	b.Termine, b.Zu, b.Ab = b.Termine+o.Termine, b.Zu+o.Zu, b.Ab+o.Ab
}

// Anwesenheit ist die Beteiligung eines Spielers im Kader. Grundlage sind die Rückmeldungen,
// nicht eine Anwesenheitskontrolle; Absagegründe fließen bewusst nicht ein.
type Anwesenheit struct {
	SpielerID string
	Vorname   string
	Nachname  string
	Training  Beteiligung
	Spiel     Beteiligung
	// Gesamt umfasst auch sonstige Termine.
	Gesamt Beteiligung
}

// Anwesenheit liefert je Spieler im Kader die Beteiligung an den Terminen der Mannschaft,
// die bereits begonnen haben. Eine Mannschaft gehört zu einer Saison, also gilt die Statistik je Saison.
func (s *Store) Anwesenheit(ctx context.Context, vereinID, mannschaftID string) ([]Anwesenheit, error) {
	var out []Anwesenheit
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT sp.id::text, sp.vorname, sp.nachname, coalesce(t.typ, ''), count(t.id)::int,
	(count(*) FILTER (WHERE r.status = 'zu'))::int, (count(*) FILTER (WHERE r.status = 'ab'))::int
FROM kader k
JOIN spieler sp ON sp.id = k.spieler_id
LEFT JOIN termin t ON t.mannschaft_id = k.mannschaft_id AND NOT t.abgesagt AND t.beginn < $2
LEFT JOIN rueckmeldung r ON r.termin_id = t.id AND r.spieler_id = sp.id
WHERE k.mannschaft_id = $1
GROUP BY sp.id, sp.vorname, sp.nachname, t.typ
ORDER BY sp.nachname, sp.vorname, sp.id`, mannschaftID, s.Now())
		if err != nil {
			return err
		}
		var id, vorname, nachname, typ string
		var b Beteiligung
		_, err = pgx.ForEachRow(rows, []any{&id, &vorname, &nachname, &typ, &b.Termine, &b.Zu, &b.Ab}, func() error {
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
