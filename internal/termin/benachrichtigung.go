package termin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ruben-sch/teamtafel/internal/nachricht"
)

// ErinnerungVorher: so lange vor der Frist werden Offene erinnert.
const ErinnerungVorher = 24 * time.Hour

// Wochentage auf Deutsch, Index ist time.Weekday.
var Wochentage = [...]string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"}

// TypName ist die Anzeige eines Termintyps.
func TypName(typ string) string {
	switch typ {
	case TypSpiel:
		return "Spiel"
	case TypSonstiges:
		return "Termin"
	}
	return "Training"
}

// kurz: "Training am Sa 17.10. um 10:00", mit Titel statt Typ, wenn vorhanden.
func kurz(d Daten) string {
	name := TypName(d.Typ)
	if d.Titel != "" {
		name += " " + d.Titel
	}
	b := d.Beginn.In(Zeitzone)
	return fmt.Sprintf("%s am %s %s um %s", name, Wochentage[b.Weekday()][:2], b.Format("02.01."), b.Format("15:04"))
}

// details: Zeit und Ort für den Text einer Benachrichtigung.
func details(d Daten) string {
	var sb strings.Builder
	b := d.Beginn.In(Zeitzone)
	fmt.Fprintf(&sb, "%s, %s, %s–%s Uhr", Wochentage[b.Weekday()], b.Format("02.01.2006"), b.Format("15:04"),
		d.Ende.In(Zeitzone).Format("15:04"))
	if d.Treffzeit != nil {
		fmt.Fprintf(&sb, "\nTreffen: %s Uhr", d.Treffzeit.In(Zeitzone).Format("15:04"))
		if d.Treffpunkt != "" {
			sb.WriteString(", " + d.Treffpunkt)
		}
	}
	if d.Ort != "" {
		sb.WriteString("\nOrt: " + d.Ort)
	}
	if d.Frist != nil {
		f := d.Frist.In(Zeitzone)
		fmt.Fprintf(&sb, "\nRückmeldung bis %s %s, %s Uhr", Wochentage[f.Weekday()][:2], f.Format("02.01."), f.Format("15:04"))
	}
	return sb.String()
}

func frist(d Daten) time.Time {
	if d.Frist != nil {
		return *d.Frist
	}
	return d.Beginn
}

// anTeam benachrichtigt die Mannschaft außer dem Auslöser.
func (s *Store) anTeam(ctx context.Context, tx pgx.Tx, vereinID, mannschaftID, von string, in nachricht.Inhalt) error {
	konten, err := nachricht.Team(ctx, tx, mannschaftID, von)
	if err != nil {
		return err
	}
	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM mannschaft WHERE id = $1`, mannschaftID).Scan(&name); err != nil {
		return err
	}
	in.Betreff = name + ": " + in.Betreff
	return nachricht.An(ctx, tx, vereinID, konten, in, s.Now())
}

// Erinnern erinnert bei allen Terminen, deren Frist in weniger als ErinnerungVorher endet,
// die Konten der noch offenen Spieler; je Termin einmal. Der Wartungsjob ruft das regelmäßig auf.
func (s *Store) Erinnern(ctx context.Context, vereinID string) error {
	jetzt := s.Now()
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
UPDATE termin SET erinnert_am = $1
WHERE erinnert_am IS NULL AND NOT abgesagt
	AND coalesce(frist, beginn) > $1 AND coalesce(frist, beginn) <= $2
RETURNING id::text, mannschaft_id::text, typ, titel, beginn, ende, treffzeit, frist, ort, treffpunkt`,
			jetzt, jetzt.Add(ErinnerungVorher))
		if err != nil {
			return err
		}
		type faellig struct {
			id, mannschaft string
			Daten
		}
		ts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (faellig, error) {
			var f faellig
			return f, r.Scan(&f.id, &f.mannschaft, &f.Typ, &f.Titel, &f.Beginn, &f.Ende, &f.Treffzeit, &f.Frist, &f.Ort, &f.Treffpunkt)
		})
		if err != nil {
			return err
		}
		for _, t := range ts {
			if err := s.offeneErinnern(ctx, tx, vereinID, t.id, t.Daten); err != nil {
				return err
			}
		}
		return nil
	})
}

// offeneErinnern schickt jedem Konto, das für offene Spieler antworten darf, eine Erinnerung mit deren Vornamen.
func (s *Store) offeneErinnern(ctx context.Context, tx pgx.Tx, vereinID, terminID string, d Daten) error {
	rows, err := tx.Query(ctx, `
SELECT a.konto_id::text, string_agg(sp.vorname, ', ' ORDER BY sp.vorname)
FROM termin t
JOIN kader k ON k.mannschaft_id = t.mannschaft_id
JOIN spieler sp ON sp.id = k.spieler_id
JOIN LATERAL (
	SELECT sp.konto_id WHERE sp.konto_id IS NOT NULL
	UNION SELECT v.konto_id FROM vertretung v WHERE v.spieler_id = sp.id AND sp.jahrgang + 18 > $2
) a ON true
WHERE t.id = $1 AND NOT EXISTS (SELECT 1 FROM rueckmeldung r WHERE r.termin_id = t.id AND r.spieler_id = sp.id)
GROUP BY a.konto_id`, terminID, s.Now().Year())
	if err != nil {
		return err
	}
	type offen struct{ konto, namen string }
	offene, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (offen, error) {
		var o offen
		return o, r.Scan(&o.konto, &o.namen)
	})
	if err != nil {
		return err
	}
	for _, o := range offene {
		in := nachricht.Inhalt{
			Betreff: "Erinnerung: " + kurz(d),
			Text:    "Für " + o.namen + " fehlt noch die Zu- oder Absage.\n\n" + details(d),
			Pfad:    "/t/" + terminID,
		}
		if err := nachricht.An(ctx, tx, vereinID, []string{o.konto}, in, s.Now()); err != nil {
			return err
		}
	}
	return nil
}
