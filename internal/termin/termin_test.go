package termin_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/dbtest"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/termin"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

var berlin = mustLoad("Europe/Berlin")

func mustLoad(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

type fixture struct {
	ctx        context.Context
	pool       *pgxpool.Pool
	store      *termin.Store
	verein     verein.Verein
	mannschaft verein.Mannschaft
	jetzt      time.Time
}

func eindeutig(p string) string { return fmt.Sprintf("%s-%d", p, time.Now().UnixNano()) }

func setup(t *testing.T) *fixture {
	t.Helper()
	pool := dbtest.AppPool(t)
	ctx := context.Background()
	vs := verein.NewStore(pool)
	v, err := vs.Anlegen(ctx, eindeutig("fc"), "FC Termin")
	if err != nil {
		t.Fatal(err)
	}
	m, err := vs.MannschaftAnlegen(ctx, v.ID, "2026/27", "E1")
	if err != nil {
		t.Fatal(err)
	}
	// Freitag, 9.10.2026, 12:00 in Berlin.
	f := &fixture{ctx: ctx, pool: pool, verein: v, mannschaft: m, jetzt: time.Date(2026, 10, 9, 12, 0, 0, 0, berlin)}
	f.store = termin.NewStore(pool)
	f.store.Now = func() time.Time { return f.jetzt }
	return f
}

func beiBerlin(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, berlin)
}

func (f *fixture) einzel(t *testing.T) termin.Termin {
	t.Helper()
	treff := beiBerlin(2026, 10, 17, 9, 15)
	tm, err := f.store.Anlegen(f.ctx, f.verein.ID, f.mannschaft.ID, termin.Daten{
		Typ: termin.TypSpiel, Titel: "gegen SV Nachbar", Beginn: beiBerlin(2026, 10, 17, 10, 0),
		Ende: beiBerlin(2026, 10, 17, 11, 30), Treffzeit: &treff, Ort: "Sportplatz", Treffpunkt: "Vereinsheim",
	})
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func TestEinzelterminAnlegenAendernAbsagen(t *testing.T) {
	f := setup(t)
	tm := f.einzel(t)

	got, err := f.store.Termin(f.ctx, f.verein.ID, tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Titel != "gegen SV Nachbar" || !got.Beginn.Equal(beiBerlin(2026, 10, 17, 10, 0)) || got.Treffzeit == nil ||
		got.MannschaftID != f.mannschaft.ID || got.Mannschaft != "E1" || got.Abgesagt || got.Bearbeitet {
		t.Fatalf("termin = %+v", got)
	}

	d := got.Daten
	d.Ort = "Kunstrasen"
	d.Treffzeit = nil
	if err := f.store.Aendern(f.ctx, f.verein.ID, tm.ID, d); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Absagen(f.ctx, f.verein.ID, tm.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = f.store.Termin(f.ctx, f.verein.ID, tm.ID)
	if got.Ort != "Kunstrasen" || got.Treffzeit != nil || !got.Bearbeitet || !got.Abgesagt {
		t.Fatalf("nach ändern und absagen: %+v", got)
	}

	if _, err := f.store.Termin(f.ctx, f.verein.ID, "01900000-0000-7000-8000-000000000000"); !errors.Is(err, termin.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestTerminValidierung(t *testing.T) {
	f := setup(t)
	b := beiBerlin(2026, 10, 17, 10, 0)
	spaet := b.Add(time.Hour)
	for name, d := range map[string]termin.Daten{
		"ende vor beginn":  {Typ: termin.TypTraining, Beginn: b, Ende: b},
		"typ unbekannt":    {Typ: "party", Beginn: b, Ende: b.Add(time.Hour)},
		"treff nach start": {Typ: termin.TypTraining, Beginn: b, Ende: b.Add(2 * time.Hour), Treffzeit: &spaet},
		"frist nach start": {Typ: termin.TypTraining, Beginn: b, Ende: b.Add(2 * time.Hour), Frist: &spaet},
	} {
		if _, err := f.store.Anlegen(f.ctx, f.verein.ID, f.mannschaft.ID, d); !errors.Is(err, termin.ErrUngueltig) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func dienstagsTraining() termin.SerieDaten {
	return termin.SerieDaten{
		Wochentag: time.Tuesday, Uhrzeit: "18:00", Dauer: 90 * time.Minute, TreffVorher: 15 * time.Minute,
		FristVorher: 24 * time.Hour, Ort: "Sportplatz", GueltigVon: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestSerieErzeugtAchtWochenImVoraus(t *testing.T) {
	f := setup(t)
	s, err := f.store.SerieAnlegen(f.ctx, f.verein.ID, f.mannschaft.ID, dienstagsTraining())
	if err != nil {
		t.Fatal(err)
	}
	ts, err := f.store.Kommende(f.ctx, f.verein.ID, f.mannschaft.ID, f.jetzt, f.jetzt.AddDate(1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	// 13.10. bis 1.12.: acht Dienstage innerhalb von 56 Tagen ab dem 9.10.
	if len(ts) != 8 {
		t.Fatalf("%d termine, erwartet 8", len(ts))
	}
	erster, nachUmstellung := ts[0], ts[3]
	if !erster.Beginn.Equal(beiBerlin(2026, 10, 13, 18, 0)) || erster.Beginn.UTC().Hour() != 16 {
		t.Fatalf("erster = %v", erster.Beginn)
	}
	// Nach dem Ende der Sommerzeit bleibt es 18 Uhr Ortszeit.
	if !nachUmstellung.Beginn.Equal(beiBerlin(2026, 11, 3, 18, 0)) || nachUmstellung.Beginn.UTC().Hour() != 17 {
		t.Fatalf("3.11. = %v", nachUmstellung.Beginn.UTC())
	}
	if erster.SerieID != s.ID || erster.Typ != termin.TypTraining || erster.Ort != "Sportplatz" ||
		!erster.Ende.Equal(beiBerlin(2026, 10, 13, 19, 30)) ||
		erster.Treffzeit == nil || !erster.Treffzeit.Equal(beiBerlin(2026, 10, 13, 17, 45)) ||
		erster.Frist == nil || !erster.Frist.Equal(beiBerlin(2026, 10, 12, 18, 0)) {
		t.Fatalf("erster = %+v", erster)
	}

	// Fortschreiben ist idempotent und ergänzt eine Woche später den nächsten Termin.
	if err := f.store.Fortschreiben(f.ctx, f.verein.ID); err != nil {
		t.Fatal(err)
	}
	f.jetzt = f.jetzt.AddDate(0, 0, 7)
	if err := f.store.Fortschreiben(f.ctx, f.verein.ID); err != nil {
		t.Fatal(err)
	}
	alle, _ := f.store.Kommende(f.ctx, f.verein.ID, f.mannschaft.ID, beiBerlin(2026, 10, 1, 0, 0), f.jetzt.AddDate(1, 0, 0))
	if len(alle) != 9 {
		t.Fatalf("%d termine nach fortschreiben, erwartet 9", len(alle))
	}
}

func TestSerieBeendenBehaeltBearbeiteteTermine(t *testing.T) {
	f := setup(t)
	s, err := f.store.SerieAnlegen(f.ctx, f.verein.ID, f.mannschaft.ID, dienstagsTraining())
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := f.store.Kommende(f.ctx, f.verein.ID, f.mannschaft.ID, f.jetzt, f.jetzt.AddDate(1, 0, 0))
	verschoben := ts[1].Daten
	verschoben.Beginn, verschoben.Ende = verschoben.Beginn.Add(time.Hour), verschoben.Ende.Add(time.Hour)
	if err := f.store.Aendern(f.ctx, f.verein.ID, ts[1].ID, verschoben); err != nil {
		t.Fatal(err)
	}

	serien, err := f.store.Serien(f.ctx, f.verein.ID, f.mannschaft.ID)
	if err != nil || len(serien) != 1 || serien[0].ID != s.ID || serien[0].Uhrzeit != "18:00" {
		t.Fatalf("serien = %+v %v", serien, err)
	}
	if err := f.store.SerieBeenden(f.ctx, f.verein.ID, s.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Fortschreiben(f.ctx, f.verein.ID); err != nil {
		t.Fatal(err)
	}
	rest, _ := f.store.Kommende(f.ctx, f.verein.ID, f.mannschaft.ID, f.jetzt, f.jetzt.AddDate(1, 0, 0))
	if len(rest) != 1 || rest[0].ID != ts[1].ID {
		t.Fatalf("nach beenden: %+v", rest)
	}
	if serien, _ := f.store.Serien(f.ctx, f.verein.ID, f.mannschaft.ID); len(serien) != 0 {
		t.Fatalf("beendete serie noch aktiv: %+v", serien)
	}
}

func TestSerieValidierung(t *testing.T) {
	f := setup(t)
	for name, mod := range map[string]func(*termin.SerieDaten){
		"uhrzeit":    func(d *termin.SerieDaten) { d.Uhrzeit = "25:00" },
		"dauer":      func(d *termin.SerieDaten) { d.Dauer = 0 },
		"bis vorher": func(d *termin.SerieDaten) { b := d.GueltigVon.AddDate(0, 0, -1); d.GueltigBis = &b },
	} {
		d := dienstagsTraining()
		mod(&d)
		if _, err := f.store.SerieAnlegen(f.ctx, f.verein.ID, f.mannschaft.ID, d); !errors.Is(err, termin.ErrUngueltig) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTermineFuerKonto(t *testing.T) {
	f := setup(t)
	tm := f.einzel(t)
	as, ts := auth.NewStore(f.pool), team.NewStore(f.pool)
	trainer, _ := as.KontoFuer(f.ctx, eindeutig("trainer")+"@example.org")
	eltern, _ := as.KontoFuer(f.ctx, eindeutig("eltern")+"@example.org")
	fremd, _ := as.KontoFuer(f.ctx, eindeutig("fremd")+"@example.org")
	if err := ts.TrainerHinzufuegen(f.ctx, f.verein.ID, f.mannschaft.ID, trainer.ID); err != nil {
		t.Fatal(err)
	}
	a, err := ts.AnfrageStellen(f.ctx, f.verein.ID, f.mannschaft.ID, eltern.ID,
		team.AnfrageDaten{Art: team.ArtKind, Vorname: "Lia", Nachname: "Ball", Jahrgang: 2017})
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.Freigeben(f.ctx, f.verein.ID, a.ID, trainer.ID, false); err != nil {
		t.Fatal(err)
	}

	for name, k := range map[string]auth.Konto{"trainer": trainer, "eltern": eltern} {
		got, err := f.store.FuerKonto(f.ctx, f.verein.ID, k.ID, f.jetzt, f.jetzt.AddDate(0, 1, 0))
		if err != nil || len(got) != 1 || got[0].ID != tm.ID {
			t.Errorf("%s: %+v %v", name, got, err)
		}
		if ok, err := f.store.GehoertZumTeam(f.ctx, f.verein.ID, f.mannschaft.ID, k.ID); err != nil || !ok {
			t.Errorf("%s gehört nicht zum team: %v", name, err)
		}
	}
	if got, _ := f.store.FuerKonto(f.ctx, f.verein.ID, fremd.ID, f.jetzt, f.jetzt.AddDate(0, 1, 0)); len(got) != 0 {
		t.Errorf("fremd sieht termine: %+v", got)
	}
	if ok, _ := f.store.GehoertZumTeam(f.ctx, f.verein.ID, f.mannschaft.ID, fremd.ID); ok {
		t.Error("fremd gehört zum team")
	}
}
