package termin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ruben-sch/teamtafel/internal/nachricht"
)

// DienstVorlauf: so lange vor Beginn geht der Hinweis „Plätze frei“ an das Team,
// und ab dann meldet Austragen den Trainern.
const DienstVorlauf = 48 * time.Hour

// ErrVoll: Alle Plätze des Dienstes sind belegt.
var ErrVoll = errors.New("dienst ist voll")

// Dienst ist eine Aufgabe an einem Termin (Kuchen, Kasse) mit einer festen Zahl an Plätzen.
type Dienst struct {
	ID          string
	TerminID    string
	Bezeichnung string
	Plaetze     int
	Eintraege   []DienstEintrag
}

// Frei ist die Zahl der offenen Plätze.
func (d Dienst) Frei() int { return d.Plaetze - len(d.Eintraege) }

// Hat meldet, ob das Konto eingetragen ist.
func (d Dienst) Hat(kontoID string) bool {
	return slices.ContainsFunc(d.Eintraege, func(e DienstEintrag) bool { return e.KontoID == kontoID })
}

// DienstEintrag ist ein belegter Platz.
type DienstEintrag struct {
	KontoID string
	Name    string
	// EingeteiltVon ist das Konto des Trainers; leer, wenn sich das Konto selbst eingetragen hat.
	EingeteiltVon string
}

// Selbst meldet, ob sich das Konto selbst eingetragen hat.
func (e DienstEintrag) Selbst() bool { return e.EingeteiltVon == "" }

// Person ist ein Konto der Mannschaft, das Dienste übernehmen kann.
type Person struct {
	KontoID string
	Name    string
}

// DienstInfo fasst die Dienste eines Termins für die Startseite zusammen.
type DienstInfo struct {
	Frei int
	// Meine sind die Bezeichnungen der Dienste, in denen das Konto steht.
	Meine []string
}

// kontoName zeigt ein Konto in der Mannschaft des Termins: Name, sonst eigener Vorname als Spieler,
// sonst „Eltern von …“ mit den Vornamen der Kinder, sonst „Trainer“. Braucht die Aliase kt (konto) und t (termin).
const kontoName = `coalesce(nullif(kt.name, ''),
	(SELECT min(sp.vorname) FROM kader kd JOIN spieler sp ON sp.id = kd.spieler_id
		WHERE kd.mannschaft_id = t.mannschaft_id AND sp.konto_id = kt.id),
	(SELECT 'Eltern von ' || string_agg(sp.vorname, ' und ' ORDER BY sp.vorname) FROM kader kd
		JOIN vertretung v ON v.spieler_id = kd.spieler_id JOIN spieler sp ON sp.id = kd.spieler_id
		WHERE kd.mannschaft_id = t.mannschaft_id AND v.konto_id = kt.id),
	CASE WHEN EXISTS (SELECT 1 FROM trainer tr WHERE tr.mannschaft_id = t.mannschaft_id AND tr.konto_id = kt.id)
		THEN 'Trainer' END,
	'ehemals im Team')`

// imTeam: Das Konto $KONTO ist Trainer der Termin-Mannschaft oder hat darin einen Spieler (selbst oder vertreten).
const imTeam = `(EXISTS (SELECT 1 FROM trainer tr WHERE tr.mannschaft_id = t.mannschaft_id AND tr.konto_id = $KONTO)
	OR EXISTS (SELECT 1 FROM kader kd JOIN spieler sp ON sp.id = kd.spieler_id WHERE kd.mannschaft_id = t.mannschaft_id
		AND (sp.konto_id = $KONTO OR EXISTS (SELECT 1 FROM vertretung v WHERE v.spieler_id = sp.id AND v.konto_id = $KONTO))))`

func mitKonto(sql, konto string) string { return strings.ReplaceAll(sql, "$KONTO", konto+"::uuid") }

func dienstPruefen(bezeichnung string, plaetze int) (string, error) {
	bezeichnung = strings.TrimSpace(bezeichnung)
	if bezeichnung == "" || len([]rune(bezeichnung)) > 80 || plaetze < 1 || plaetze > 20 {
		return "", fmt.Errorf("%w: dienst %q mit %d plätzen", ErrUngueltig, bezeichnung, plaetze)
	}
	return bezeichnung, nil
}

// vortagAbend: 18 Uhr am Tag vor dem Beginn, dann kommt die Erinnerung an die Eingetragenen.
func vortagAbend(beginn time.Time) time.Time {
	b := beginn.In(Zeitzone)
	return time.Date(b.Year(), b.Month(), b.Day()-1, 18, 0, 0, 0, Zeitzone)
}

// DienstAnlegen legt einen Dienst am Termin an; die Berechtigung (Trainer) prüft der Aufrufer.
func (s *Store) DienstAnlegen(ctx context.Context, vereinID, terminID, bezeichnung string, plaetze int) (Dienst, error) {
	bezeichnung, err := dienstPruefen(bezeichnung, plaetze)
	if err != nil {
		return Dienst{}, err
	}
	d := Dienst{TerminID: terminID, Bezeichnung: bezeichnung, Plaetze: plaetze}
	err = s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
INSERT INTO dienst (verein_id, termin_id, bezeichnung, plaetze) VALUES ($1, $2, $3, $4) RETURNING id::text`,
			vereinID, terminID, bezeichnung, plaetze).Scan(&d.ID)
	})
	if err != nil {
		return Dienst{}, fmt.Errorf("dienst anlegen: %w", err)
	}
	return d, nil
}

// DienstAendern ändert Bezeichnung und Plätze; weniger Plätze als Einträge gehen nicht.
func (s *Store) DienstAendern(ctx context.Context, vereinID, terminID, dienstID, bezeichnung string, plaetze int) error {
	bezeichnung, err := dienstPruefen(bezeichnung, plaetze)
	if err != nil {
		return err
	}
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		var belegt int
		err := tx.QueryRow(ctx, `
SELECT (SELECT count(*) FROM dienst_eintrag e WHERE e.dienst_id = d.id)::int
FROM dienst d WHERE d.id = $1 AND d.termin_id = $2 FOR UPDATE`, dienstID, terminID).Scan(&belegt)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return err
		case plaetze < belegt:
			return fmt.Errorf("%w: %d plätze bei %d einträgen", ErrUngueltig, plaetze, belegt)
		}
		_, err = tx.Exec(ctx, `UPDATE dienst SET bezeichnung = $2, plaetze = $3 WHERE id = $1`, dienstID, bezeichnung, plaetze)
		return err
	})
}

// DienstLoeschen entfernt den Dienst samt Einträgen.
func (s *Store) DienstLoeschen(ctx context.Context, vereinID, terminID, dienstID string) error {
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM dienst WHERE id = $1 AND termin_id = $2`, dienstID, terminID)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

// dienstKopf sind Dienst und Termin, gesperrt für die Änderung eines Eintrags.
type dienstKopf struct {
	bezeichnung, mannschaft string
	plaetze, belegt         int
	abgesagt, imTeam, schon bool
	name                    string
	Daten
}

func (s *Store) dienstKopf(ctx context.Context, tx pgx.Tx, terminID, dienstID, kontoID string) (dienstKopf, error) {
	var k dienstKopf
	err := tx.QueryRow(ctx, `
SELECT d.bezeichnung, d.plaetze, (SELECT count(*) FROM dienst_eintrag e WHERE e.dienst_id = d.id)::int,
	EXISTS (SELECT 1 FROM dienst_eintrag e WHERE e.dienst_id = d.id AND e.konto_id = $3),
	t.mannschaft_id::text, t.abgesagt, `+mitKonto(imTeam, "$3")+`,
	coalesce((SELECT `+kontoName+` FROM konto kt WHERE kt.id = $3), ''),
	t.typ, t.titel, t.beginn, t.ende, t.treffzeit, t.frist, t.ort, t.treffpunkt
FROM dienst d JOIN termin t ON t.id = d.termin_id
WHERE d.id = $1 AND d.termin_id = $2
FOR UPDATE OF d`, dienstID, terminID, kontoID).Scan(&k.bezeichnung, &k.plaetze, &k.belegt, &k.schon,
		&k.mannschaft, &k.abgesagt, &k.imTeam, &k.name,
		&k.Typ, &k.Titel, &k.Beginn, &k.Ende, &k.Treffzeit, &k.Frist, &k.Ort, &k.Treffpunkt)
	if errors.Is(err, pgx.ErrNoRows) {
		return k, ErrNotFound
	}
	return k, err
}

// Eintragen belegt einen Platz für kontoID. Ist vonKontoID ein anderes Konto, hat ein Trainer
// eingeteilt (die Berechtigung prüft der Aufrufer) und die Person wird benachrichtigt.
// Eintragen geht bis zum Beginn und nur für Konten der Mannschaft; doppelt eintragen ändert nichts.
func (s *Store) Eintragen(ctx context.Context, vereinID, terminID, dienstID, kontoID, vonKontoID string) error {
	jetzt := s.Now()
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		k, err := s.dienstKopf(ctx, tx, terminID, dienstID, kontoID)
		switch {
		case err != nil:
			return err
		case k.abgesagt:
			return ErrAbgesagt
		case !k.imTeam:
			return ErrNichtBerechtigt
		case !jetzt.Before(k.Beginn):
			return ErrFristVorbei
		case k.schon:
			return nil
		case k.belegt >= k.plaetze:
			return ErrVoll
		}
		var von *string
		if vonKontoID != kontoID {
			von = &vonKontoID
		}
		// Nach 18 Uhr am Vortag gibt es keine eigene Erinnerung mehr.
		var erinnert *time.Time
		if !jetzt.Before(vortagAbend(k.Beginn)) {
			erinnert = &jetzt
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO dienst_eintrag (verein_id, dienst_id, konto_id, eingeteilt_von, erinnert_am, created_at)
VALUES ($1, $2, $3, $4, $5, $6)`, vereinID, dienstID, kontoID, von, erinnert, jetzt); err != nil {
			return err
		}
		if von == nil {
			return nil
		}
		return nachricht.An(ctx, tx, vereinID, []string{kontoID}, nachricht.Inhalt{
			Betreff: "Eingeteilt: " + k.bezeichnung + ", " + kurz(k.Daten),
			Text: "Du bist für „" + k.bezeichnung + "“ eingeteilt. Wenn es nicht passt, trag dich in der App aus.\n\n" +
				details(k.Daten),
			Pfad: "/t/" + terminID,
		}, jetzt)
	})
}

// Austragen gibt den Platz von kontoID frei. Trägt sich jemand selbst weniger als DienstVorlauf
// vor Beginn aus, erfahren es die Trainer. Fremde Einträge entfernt nur ein Trainer (prüft der Aufrufer).
func (s *Store) Austragen(ctx context.Context, vereinID, terminID, dienstID, kontoID, vonKontoID string) error {
	jetzt := s.Now()
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		k, err := s.dienstKopf(ctx, tx, terminID, dienstID, kontoID)
		if err != nil || !k.schon {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM dienst_eintrag WHERE dienst_id = $1 AND konto_id = $2`, dienstID, kontoID); err != nil {
			return err
		}
		if kontoID != vonKontoID || k.abgesagt || !jetzt.Before(k.Beginn) || k.Beginn.Sub(jetzt) > DienstVorlauf {
			return nil
		}
		trainer, err := nachricht.Trainer(ctx, tx, k.mannschaft, vonKontoID)
		if err != nil {
			return err
		}
		return s.mitMannschaft(ctx, tx, vereinID, k.mannschaft, trainer, nachricht.Inhalt{
			Betreff: k.name + " hat sich ausgetragen: " + k.bezeichnung + ", " + kurz(k.Daten),
			Text:    "Für „" + k.bezeichnung + "“ ist wieder ein Platz frei.\n\n" + details(k.Daten),
			Pfad:    "/t/" + terminID,
		})
	})
}

// mitMannschaft schickt die Nachricht mit dem Mannschaftsnamen vor dem Betreff.
func (s *Store) mitMannschaft(ctx context.Context, tx pgx.Tx, vereinID, mannschaftID string, konten []string,
	in nachricht.Inhalt) error {
	if len(konten) == 0 {
		return nil
	}
	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM mannschaft WHERE id = $1`, mannschaftID).Scan(&name); err != nil {
		return err
	}
	in.Betreff = name + ": " + in.Betreff
	return nachricht.An(ctx, tx, vereinID, konten, in, s.Now())
}

// Dienste listet die Dienste eines Termins mit ihren Einträgen, in der Reihenfolge des Anlegens.
func (s *Store) Dienste(ctx context.Context, vereinID, terminID string) ([]Dienst, error) {
	var out []Dienst
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT d.id::text, d.bezeichnung, d.plaetze, coalesce(e.konto_id::text, ''), coalesce(e.eingeteilt_von::text, ''),
	CASE WHEN e.konto_id IS NULL THEN '' ELSE `+kontoName+` END
FROM dienst d
JOIN termin t ON t.id = d.termin_id
LEFT JOIN dienst_eintrag e ON e.dienst_id = d.id
LEFT JOIN konto kt ON kt.id = e.konto_id
WHERE d.termin_id = $1
ORDER BY d.created_at, d.id, e.created_at`, terminID)
		if err != nil {
			return err
		}
		var id, bezeichnung string
		var plaetze int
		var e DienstEintrag
		_, err = pgx.ForEachRow(rows, []any{&id, &bezeichnung, &plaetze, &e.KontoID, &e.EingeteiltVon, &e.Name}, func() error {
			if len(out) == 0 || out[len(out)-1].ID != id {
				out = append(out, Dienst{ID: id, TerminID: terminID, Bezeichnung: bezeichnung, Plaetze: plaetze})
			}
			if e.KontoID != "" {
				d := &out[len(out)-1]
				d.Eintraege = append(d.Eintraege, e)
			}
			return nil
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("dienste laden: %w", err)
	}
	return out, nil
}

// DienstPersonen listet die Konten der Termin-Mannschaft, die ein Trainer einteilen kann.
func (s *Store) DienstPersonen(ctx context.Context, vereinID, terminID string) ([]Person, error) {
	var out []Person
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT kt.id::text, `+kontoName+` AS name
FROM termin t JOIN konto kt ON `+mitKonto(imTeam, "kt.id")+`
WHERE t.id = $1
ORDER BY name, kt.id`, terminID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Person, error) {
			var p Person
			return p, r.Scan(&p.KontoID, &p.Name)
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("personen laden: %w", err)
	}
	return out, nil
}

// DienstUebersicht liefert je Termin mit Diensten die freien Plätze und die Dienste des Kontos.
func (s *Store) DienstUebersicht(ctx context.Context, vereinID, kontoID string, terminIDs []string) (map[string]DienstInfo, error) {
	out := map[string]DienstInfo{}
	if len(terminIDs) == 0 {
		return out, nil
	}
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT d.termin_id::text, d.bezeichnung, d.plaetze - (SELECT count(*) FROM dienst_eintrag e WHERE e.dienst_id = d.id)::int,
	EXISTS (SELECT 1 FROM dienst_eintrag e WHERE e.dienst_id = d.id AND e.konto_id = $2)
FROM dienst d WHERE d.termin_id = ANY($1::uuid[])
ORDER BY d.created_at, d.id`, terminIDs, kontoID)
		if err != nil {
			return err
		}
		var terminID, bezeichnung string
		var frei int
		var meins bool
		_, err = pgx.ForEachRow(rows, []any{&terminID, &bezeichnung, &frei, &meins}, func() error {
			i := out[terminID]
			i.Frei += frei
			if meins {
				i.Meine = append(i.Meine, bezeichnung)
			}
			out[terminID] = i
			return nil
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("dienstübersicht laden: %w", err)
	}
	return out, nil
}

// DiensteErinnern schickt DienstVorlauf vor Beginn je Termin einmal den Hinweis „Plätze frei“ an alle
// im Team, die noch in keinem Dienst des Termins stehen, und um 18 Uhr am Vortag jedem Eingetragenen
// eine Erinnerung mit seinen Diensten. Die Wartung ruft das regelmäßig auf.
func (s *Store) DiensteErinnern(ctx context.Context, vereinID string) error {
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		if err := s.dienstHinweise(ctx, tx, vereinID); err != nil {
			return err
		}
		return s.dienstErinnerungen(ctx, tx, vereinID)
	})
}

func (s *Store) dienstHinweise(ctx context.Context, tx pgx.Tx, vereinID string) error {
	jetzt := s.Now()
	rows, err := tx.Query(ctx, `
UPDATE termin t SET dienst_hinweis_am = $1
WHERE dienst_hinweis_am IS NULL AND NOT abgesagt AND beginn > $1 AND beginn <= $2
	AND EXISTS (SELECT 1 FROM dienst d WHERE d.termin_id = t.id
		AND d.plaetze > (SELECT count(*) FROM dienst_eintrag e WHERE e.dienst_id = d.id))
RETURNING id::text, mannschaft_id::text, typ, titel, beginn, ende, treffzeit, frist, ort, treffpunkt`,
		jetzt, jetzt.Add(DienstVorlauf))
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
		ds, err := s.dienste(ctx, tx, t.id)
		if err != nil {
			return err
		}
		team, err := nachricht.Team(ctx, tx, t.mannschaft, "")
		if err != nil {
			return err
		}
		frei := 0
		var sb strings.Builder
		sb.WriteString("Für diese Dienste werden noch Helfer gesucht:\n")
		for _, d := range ds {
			if d.Frei() > 0 {
				frei += d.Frei()
				fmt.Fprintf(&sb, "- %s (%s)\n", d.Bezeichnung, plaetzeText(d.Frei()))
			}
			team = slices.DeleteFunc(team, d.Hat)
		}
		if err := s.mitMannschaft(ctx, tx, vereinID, t.mannschaft, team, nachricht.Inhalt{
			Betreff: "Dienste: " + plaetzeText(frei) + ", " + kurz(t.Daten),
			Text:    sb.String() + "\n" + details(t.Daten),
			Pfad:    "/t/" + t.id,
		}); err != nil {
			return err
		}
	}
	return nil
}

// plaetzeText: "1 Platz frei", "3 Plätze frei".
func plaetzeText(n int) string {
	if n == 1 {
		return "1 Platz frei"
	}
	return fmt.Sprintf("%d Plätze frei", n)
}

// dienste ist Dienste innerhalb einer laufenden Transaktion, ohne Namen.
func (s *Store) dienste(ctx context.Context, tx pgx.Tx, terminID string) ([]Dienst, error) {
	rows, err := tx.Query(ctx, `
SELECT d.id::text, d.bezeichnung, d.plaetze, coalesce(array_agg(e.konto_id::text ORDER BY e.created_at, e.konto_id) FILTER (WHERE e.konto_id IS NOT NULL), '{}')
FROM dienst d LEFT JOIN dienst_eintrag e ON e.dienst_id = d.id
WHERE d.termin_id = $1
GROUP BY d.id ORDER BY d.created_at, d.id`, terminID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Dienst, error) {
		d := Dienst{TerminID: terminID}
		var konten []string
		err := r.Scan(&d.ID, &d.Bezeichnung, &d.Plaetze, &konten)
		for _, k := range konten {
			d.Eintraege = append(d.Eintraege, DienstEintrag{KontoID: k})
		}
		return d, err
	})
}

func (s *Store) dienstErinnerungen(ctx context.Context, tx pgx.Tx, vereinID string) error {
	jetzt := s.Now()
	rows, err := tx.Query(ctx, `
UPDATE dienst_eintrag e SET erinnert_am = $1
FROM dienst d JOIN termin t ON t.id = d.termin_id
WHERE e.dienst_id = d.id AND e.erinnert_am IS NULL AND NOT t.abgesagt AND t.beginn > $1
	AND (date_trunc('day', t.beginn AT TIME ZONE 'Europe/Berlin') - interval '6 hours') AT TIME ZONE 'Europe/Berlin' <= $1
RETURNING e.konto_id::text, t.id::text, d.bezeichnung, t.typ, t.titel, t.beginn, t.ende, t.treffzeit, t.frist, t.ort, t.treffpunkt`,
		jetzt)
	if err != nil {
		return err
	}
	type faellig struct {
		konto, termin, bezeichnung string
		Daten
	}
	fs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (faellig, error) {
		var f faellig
		return f, r.Scan(&f.konto, &f.termin, &f.bezeichnung, &f.Typ, &f.Titel, &f.Beginn, &f.Ende, &f.Treffzeit, &f.Frist,
			&f.Ort, &f.Treffpunkt)
	})
	if err != nil {
		return err
	}
	// Eine Nachricht je Konto und Termin mit allen Diensten.
	slices.SortFunc(fs, func(a, b faellig) int {
		return strings.Compare(a.konto+a.termin+a.bezeichnung, b.konto+b.termin+b.bezeichnung)
	})
	for i := 0; i < len(fs); {
		j := i
		var bez []string
		for ; j < len(fs) && fs[j].konto == fs[i].konto && fs[j].termin == fs[i].termin; j++ {
			bez = append(bez, fs[j].bezeichnung)
		}
		f := fs[i]
		if err := nachricht.An(ctx, tx, vereinID, []string{f.konto}, nachricht.Inhalt{
			Betreff: "Morgen: " + strings.Join(bez, ", ") + ", " + kurz(f.Daten),
			Text:    "Du hast morgen Dienst: " + strings.Join(bez, ", ") + ".\n\n" + details(f.Daten),
			Pfad:    "/t/" + f.termin,
		}, jetzt); err != nil {
			return err
		}
		i = j
	}
	return nil
}
