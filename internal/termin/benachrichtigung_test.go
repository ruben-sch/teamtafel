package termin_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/nachricht"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/termin"
)

// betreffe liefert die Betreffzeilen aller Benachrichtigungen an das Konto, die prefix enthalten.
func betreffe(t *testing.T, k *kader, konto auth.Konto, teil string) []string {
	t.Helper()
	rows, _ := k.pool.Query(k.ctx, `SELECT payload FROM job WHERE art = $1 AND payload->>'konto_id' = $2 ORDER BY created_at`,
		nachricht.Art, konto.ID)
	ps, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range ps {
		var in nachricht.Inhalt
		_ = json.Unmarshal(p, &in)
		if strings.Contains(in.Betreff, teil) {
			out = append(out, in.Betreff)
		}
	}
	return out
}

func anzahl(t *testing.T, k *kader, konto auth.Konto, teil string) int {
	t.Helper()
	return len(betreffe(t, k, konto, teil))
}

func TestNeuerTerminBenachrichtigtTeamOhneAusloeser(t *testing.T) {
	k := mitKader(t)
	treff := beiBerlin(2026, 10, 17, 9, 15)
	tm, err := k.store.Anlegen(k.ctx, k.verein.ID, k.mannschaft.ID, k.trainer.ID, termin.Daten{
		Typ: termin.TypSpiel, Titel: "gegen SV Nachbar", Beginn: beiBerlin(2026, 10, 17, 10, 0),
		Ende: beiBerlin(2026, 10, 17, 11, 30), Treffzeit: &treff,
	})
	if err != nil {
		t.Fatal(err)
	}
	b := betreffe(t, k, k.eltern, "Neu:")
	if want := "E1: Neu: Spiel gegen SV Nachbar am Sa 17.10. um 10:00"; !slices.Equal(b, []string{want}) {
		t.Errorf("eltern: %q, want %q", b, want)
	}
	if anzahl(t, k, k.selbst, "Neu:") != 1 || anzahl(t, k, k.trainer, "Neu:") != 0 || anzahl(t, k, k.fremd, "") != 0 {
		t.Error("empfänger falsch")
	}
	_ = tm

	// Vergangene Termine und Serientermine lösen nichts aus.
	_, _ = k.store.Anlegen(k.ctx, k.verein.ID, k.mannschaft.ID, k.trainer.ID, termin.Daten{
		Typ: termin.TypTraining, Beginn: k.jetzt.Add(-48 * time.Hour), Ende: k.jetzt.Add(-47 * time.Hour)})
	if _, err := k.store.SerieAnlegen(k.ctx, k.verein.ID, k.mannschaft.ID, termin.SerieDaten{
		Wochentag: time.Tuesday, Uhrzeit: "17:00", Dauer: time.Hour, GueltigVon: k.jetzt}); err != nil {
		t.Fatal(err)
	}
	if n := anzahl(t, k, k.eltern, "Neu:"); n != 1 {
		t.Errorf("nach vergangenem Termin und Serie: %d Neu-Nachrichten", n)
	}
}

func TestAenderungNurBeiZeitOderOrt(t *testing.T) {
	k := mitKader(t)
	tm := k.einzel(t)
	d := tm.Daten
	d.Titel = "gegen FC Andere"
	if err := k.store.Aendern(k.ctx, k.verein.ID, tm.ID, k.trainer.ID, d); err != nil {
		t.Fatal(err)
	}
	if n := anzahl(t, k, k.eltern, "Geändert"); n != 0 {
		t.Errorf("nur Titel geändert: %d Nachrichten", n)
	}
	d.Ort = "Kunstrasen"
	if err := k.store.Aendern(k.ctx, k.verein.ID, tm.ID, k.trainer.ID, d); err != nil {
		t.Fatal(err)
	}
	if n := anzahl(t, k, k.eltern, "Geändert"); n != 1 {
		t.Errorf("Ort geändert: %d Nachrichten", n)
	}
}

func TestAbsageBenachrichtigtEinmal(t *testing.T) {
	k := mitKader(t)
	tm := k.einzel(t)
	for range 2 {
		if err := k.store.Absagen(k.ctx, k.verein.ID, tm.ID, k.trainer.ID); err != nil {
			t.Fatal(err)
		}
	}
	if n := anzahl(t, k, k.eltern, "Abgesagt"); n != 1 {
		t.Errorf("%d Absage-Nachrichten", n)
	}
	if n := anzahl(t, k, k.trainer, "Abgesagt"); n != 0 {
		t.Errorf("Auslöser benachrichtigt")
	}
}

func TestRueckmeldungInformiertAndereVertreter(t *testing.T) {
	k := mitKader(t)
	ts := team.NewStore(k.pool)
	zweite, _ := auth.NewStore(k.pool).KontoFuer(k.ctx, eindeutig("k2")+"@example.org")
	a, err := ts.AnfrageStellen(k.ctx, k.verein.ID, k.mannschaft.ID, zweite.ID,
		team.AnfrageDaten{Art: team.ArtKind, Vorname: "Kim", Nachname: "Test", Jahrgang: 2017})
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.Freigeben(k.ctx, k.verein.ID, a.ID, k.trainer.ID, true); err != nil {
		t.Fatal(err)
	}
	tm := k.einzel(t)
	for range 2 {
		if err := k.store.Rueckmelden(k.ctx, k.verein.ID, tm.ID, k.kind, k.eltern.ID, termin.Ab, termin.GrundKrank, false); err != nil {
			t.Fatal(err)
		}
	}
	b := betreffe(t, k, zweite, "Kim hat")
	if len(b) != 1 || !strings.HasPrefix(b[0], "Kim hat abgesagt: ") || strings.Contains(b[0], "krank") {
		t.Errorf("zweite vertretung: %q", b)
	}
	if anzahl(t, k, k.eltern, "Kim hat") != 0 || anzahl(t, k, k.trainer, "Kim hat") != 0 {
		t.Error("auslöser oder trainer vor der frist benachrichtigt")
	}
}

func TestAbsageNachFristAnTrainer(t *testing.T) {
	k := mitKader(t)
	ts := team.NewStore(k.pool)
	co, _ := auth.NewStore(k.pool).KontoFuer(k.ctx, eindeutig("co")+"@example.org")
	_ = ts.TrainerHinzufuegen(k.ctx, k.verein.ID, k.mannschaft.ID, co.ID)
	tm := k.einzel(t)
	if err := k.store.Rueckmelden(k.ctx, k.verein.ID, tm.ID, k.eigener, k.selbst.ID, termin.Zu, "", false); err != nil {
		t.Fatal(err)
	}
	k.jetzt = tm.Beginn // ohne Frist gilt der Beginn
	if err := k.store.Rueckmelden(k.ctx, k.verein.ID, tm.ID, k.eigener, k.trainer.ID, termin.Ab, termin.GrundUrlaub, true); err != nil {
		t.Fatal(err)
	}
	if anzahl(t, k, co, "Absage nach Frist: Sam") != 1 || anzahl(t, k, k.trainer, "Absage nach Frist") != 0 {
		t.Errorf("co: %q, trainer: %q", betreffe(t, k, co, ""), betreffe(t, k, k.trainer, "Frist"))
	}
	if anzahl(t, k, k.selbst, "Absage nach Frist: Sam") != 1 {
		t.Errorf("spieler selbst: %q", betreffe(t, k, k.selbst, ""))
	}
}

func TestErinnerungVorFristEinmalAnOffene(t *testing.T) {
	k := mitKader(t)
	frist := beiBerlin(2026, 10, 16, 18, 0)
	tm, err := k.store.Anlegen(k.ctx, k.verein.ID, k.mannschaft.ID, k.trainer.ID, termin.Daten{
		Typ: termin.TypTraining, Beginn: beiBerlin(2026, 10, 17, 10, 0), Ende: beiBerlin(2026, 10, 17, 11, 0), Frist: &frist})
	if err != nil {
		t.Fatal(err)
	}
	if err := k.store.Rueckmelden(k.ctx, k.verein.ID, tm.ID, k.eigener, k.selbst.ID, termin.Zu, "", false); err != nil {
		t.Fatal(err)
	}
	// Mehr als 24 Stunden vor der Frist: noch nichts.
	if err := k.store.Erinnern(k.ctx, k.verein.ID); err != nil {
		t.Fatal(err)
	}
	if n := anzahl(t, k, k.eltern, "Erinnerung"); n != 0 {
		t.Fatalf("zu früh erinnert: %d", n)
	}
	k.jetzt = frist.Add(-23 * time.Hour)
	for range 2 {
		if err := k.store.Erinnern(k.ctx, k.verein.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Kim ist offen; Alex ist volljährig, dafür darf niemand antworten; Sam hat zugesagt.
	if b := betreffe(t, k, k.eltern, "Erinnerung"); len(b) != 1 {
		t.Errorf("eltern: %q", b)
	}
	if anzahl(t, k, k.selbst, "Erinnerung") != 0 || anzahl(t, k, k.trainer, "Erinnerung") != 0 {
		t.Error("falsche empfänger")
	}

	// Frist verschoben: es darf erneut erinnert werden.
	neu := frist.Add(72 * time.Hour)
	d := tm.Daten
	d.Beginn, d.Ende, d.Frist = neu.Add(time.Hour), neu.Add(2*time.Hour), &neu
	if err := k.store.Aendern(k.ctx, k.verein.ID, tm.ID, k.trainer.ID, d); err != nil {
		t.Fatal(err)
	}
	k.jetzt = neu.Add(-time.Hour)
	_ = k.store.Erinnern(k.ctx, k.verein.ID)
	if n := anzahl(t, k, k.eltern, "Erinnerung"); n != 2 {
		t.Errorf("nach verschobener frist: %d erinnerungen", n)
	}
}

func TestKurzfristigerTerminOhneZusaetzlicheErinnerung(t *testing.T) {
	k := mitKader(t)
	_, err := k.store.Anlegen(k.ctx, k.verein.ID, k.mannschaft.ID, k.trainer.ID, termin.Daten{
		Typ: termin.TypTraining, Beginn: k.jetzt.Add(5 * time.Hour), Ende: k.jetzt.Add(6 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	_ = k.store.Erinnern(k.ctx, k.verein.ID)
	if anzahl(t, k, k.eltern, "Neu:") != 1 || anzahl(t, k, k.eltern, "Erinnerung") != 0 {
		t.Errorf("eltern: %q", betreffe(t, k, k.eltern, ""))
	}
}
