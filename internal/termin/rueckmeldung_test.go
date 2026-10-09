package termin_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/termin"
)

// kader: Trainer, Eltern mit Kind (2017) und erwachsenem Kind (2000), ein Spieler mit eigenem Konto.
type kader struct {
	*fixture
	trainer, eltern, selbst, fremd auth.Konto
	kind, erwachsen, eigener       string // Spieler-IDs
}

func mitKader(t *testing.T) *kader {
	t.Helper()
	f := setup(t)
	as, ts := auth.NewStore(f.pool), team.NewStore(f.pool)
	k := &kader{fixture: f}
	for _, p := range []*auth.Konto{&k.trainer, &k.eltern, &k.selbst, &k.fremd} {
		var err error
		if *p, err = as.KontoFuer(f.ctx, eindeutig("k")+"@example.org"); err != nil {
			t.Fatal(err)
		}
	}
	if err := ts.TrainerHinzufuegen(f.ctx, f.verein.ID, f.mannschaft.ID, k.trainer.ID); err != nil {
		t.Fatal(err)
	}
	aufnehmen := func(konto auth.Konto, art, vorname string, jahrgang int) string {
		a, err := ts.AnfrageStellen(f.ctx, f.verein.ID, f.mannschaft.ID, konto.ID,
			team.AnfrageDaten{Art: art, Vorname: vorname, Nachname: "Test", Jahrgang: jahrgang})
		if err != nil {
			t.Fatal(err)
		}
		if err := ts.Freigeben(f.ctx, f.verein.ID, a.ID, k.trainer.ID, false); err != nil {
			t.Fatal(err)
		}
		sp, _ := ts.Kader(f.ctx, f.verein.ID, f.mannschaft.ID)
		for _, s := range sp {
			if s.Vorname == vorname {
				return s.ID
			}
		}
		t.Fatalf("%s nicht im kader", vorname)
		return ""
	}
	k.kind = aufnehmen(k.eltern, team.ArtKind, "Kim", 2017)
	k.erwachsen = aufnehmen(k.eltern, team.ArtKind, "Alex", 2000)
	k.eigener = aufnehmen(k.selbst, team.ArtSelbst, "Sam", 1990)
	return k
}

func status(t *testing.T, k *kader, terminID, spielerID string) termin.Rueckmeldung {
	t.Helper()
	rs, err := k.store.Rueckmeldungen(k.ctx, k.verein.ID, terminID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		if r.SpielerID == spielerID {
			return r
		}
	}
	t.Fatalf("spieler %s fehlt in %+v", spielerID, rs)
	return termin.Rueckmeldung{}
}

func TestElternSagenFuerKindZuUndAb(t *testing.T) {
	k := mitKader(t)
	tm := k.einzel(t)
	if r := status(t, k, tm.ID, k.kind); r.Status != "" || r.Vorname != "Kim" {
		t.Fatalf("vorher: %+v", r)
	}
	if err := k.store.Rueckmelden(k.ctx, k.verein.ID, tm.ID, k.kind, k.eltern.ID, termin.Zu, "", false); err != nil {
		t.Fatal(err)
	}
	if r := status(t, k, tm.ID, k.kind); r.Status != termin.Zu {
		t.Fatalf("nach zusage: %+v", r)
	}
	if err := k.store.Rueckmelden(k.ctx, k.verein.ID, tm.ID, k.kind, k.eltern.ID, termin.Ab, termin.GrundKrank, false); err != nil {
		t.Fatal(err)
	}
	if r := status(t, k, tm.ID, k.kind); r.Status != termin.Ab || r.Grund != termin.GrundKrank {
		t.Fatalf("nach absage: %+v", r)
	}

	// Zähler auf der Terminliste: 3 im Kader, 1 Absage.
	ts, _ := k.store.Kommende(k.ctx, k.verein.ID, k.mannschaft.ID, k.jetzt, k.jetzt.AddDate(0, 1, 0))
	if len(ts) != 1 || ts[0].Zu != 0 || ts[0].Ab != 1 || ts[0].Offen != 2 {
		t.Fatalf("zähler: %+v", ts)
	}
}

func TestWerDarfRueckmelden(t *testing.T) {
	k := mitKader(t)
	tm := k.einzel(t)
	fall := func(spieler string, konto auth.Konto, trainer bool, want error) {
		t.Helper()
		err := k.store.Rueckmelden(k.ctx, k.verein.ID, tm.ID, spieler, konto.ID, termin.Zu, "", trainer)
		if !errors.Is(err, want) {
			t.Errorf("err = %v, want %v", err, want)
		}
	}
	fall(k.eigener, k.selbst, false, nil)
	// Vertretung gilt nur, solange der Spieler minderjährig ist.
	fall(k.erwachsen, k.eltern, false, termin.ErrNichtBerechtigt)
	fall(k.kind, k.fremd, false, termin.ErrNichtBerechtigt)
	fall(k.kind, k.selbst, false, termin.ErrNichtBerechtigt)
	// Trainer dürfen für jeden im Kader.
	fall(k.erwachsen, k.trainer, true, nil)

	if err := k.store.Rueckmelden(k.ctx, k.verein.ID, tm.ID, k.kind, k.eltern.ID, termin.Zu, termin.GrundKrank, false); !errors.Is(err, termin.ErrUngueltig) {
		t.Errorf("grund bei zusage: %v", err)
	}
	if err := k.store.Rueckmelden(k.ctx, k.verein.ID, tm.ID, k.kind, k.eltern.ID, "vielleicht", "", false); !errors.Is(err, termin.ErrUngueltig) {
		t.Errorf("status vielleicht: %v", err)
	}
}

func TestFristUndAbsage(t *testing.T) {
	k := mitKader(t)
	frist := k.jetzt.Add(-time.Hour)
	tm, err := k.store.Anlegen(k.ctx, k.verein.ID, k.mannschaft.ID, termin.Daten{
		Typ: termin.TypTraining, Beginn: k.jetzt.Add(5 * time.Hour), Ende: k.jetzt.Add(6 * time.Hour), Frist: &frist,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := k.store.Rueckmelden(k.ctx, k.verein.ID, tm.ID, k.kind, k.eltern.ID, termin.Ab, "", false); !errors.Is(err, termin.ErrFristVorbei) {
		t.Fatalf("eltern nach frist: %v", err)
	}
	if err := k.store.Rueckmelden(k.ctx, k.verein.ID, tm.ID, k.kind, k.trainer.ID, termin.Ab, termin.GrundUrlaub, true); err != nil {
		t.Fatalf("trainer nach frist: %v", err)
	}

	// Ohne Frist endet die Rückmeldung mit dem Beginn.
	vorbei, _ := k.store.Anlegen(k.ctx, k.verein.ID, k.mannschaft.ID, termin.Daten{
		Typ: termin.TypTraining, Beginn: k.jetzt.Add(-time.Hour), Ende: k.jetzt.Add(time.Hour),
	})
	if err := k.store.Rueckmelden(k.ctx, k.verein.ID, vorbei.ID, k.kind, k.eltern.ID, termin.Zu, "", false); !errors.Is(err, termin.ErrFristVorbei) {
		t.Fatalf("eltern nach beginn: %v", err)
	}

	if err := k.store.Absagen(k.ctx, k.verein.ID, tm.ID); err != nil {
		t.Fatal(err)
	}
	if err := k.store.Rueckmelden(k.ctx, k.verein.ID, tm.ID, k.kind, k.trainer.ID, termin.Zu, "", true); !errors.Is(err, termin.ErrAbgesagt) {
		t.Fatalf("abgesagt: %v", err)
	}
}

func TestMeineSpielerJeTermin(t *testing.T) {
	k := mitKader(t)
	tm := k.einzel(t)
	_ = k.store.Rueckmelden(k.ctx, k.verein.ID, tm.ID, k.kind, k.eltern.ID, termin.Zu, "", false)
	m, err := k.store.MeineSpieler(k.ctx, k.verein.ID, k.eltern.ID, []string{tm.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Nur das minderjährige Kind; für den Erwachsenen darf das Elternteil nicht antworten.
	if got := m[tm.ID]; len(got) != 1 || got[0].SpielerID != k.kind || got[0].Status != termin.Zu || got[0].Vorname != "Kim" {
		t.Fatalf("meine spieler = %+v", m)
	}
	if m, _ := k.store.MeineSpieler(k.ctx, k.verein.ID, k.trainer.ID, []string{tm.ID}); len(m[tm.ID]) != 0 {
		t.Fatalf("trainer ohne eigene spieler: %+v", m)
	}
}

func TestSerieBeendenBehaeltTermineMitRueckmeldung(t *testing.T) {
	k := mitKader(t)
	s, err := k.store.SerieAnlegen(k.ctx, k.verein.ID, k.mannschaft.ID, dienstagsTraining())
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := k.store.Kommende(k.ctx, k.verein.ID, k.mannschaft.ID, k.jetzt, k.jetzt.AddDate(1, 0, 0))
	if err := k.store.Rueckmelden(k.ctx, k.verein.ID, ts[2].ID, k.kind, k.eltern.ID, termin.Zu, "", false); err != nil {
		t.Fatal(err)
	}
	if err := k.store.SerieBeenden(k.ctx, k.verein.ID, s.ID); err != nil {
		t.Fatal(err)
	}
	rest, _ := k.store.Kommende(k.ctx, k.verein.ID, k.mannschaft.ID, k.jetzt, k.jetzt.AddDate(1, 0, 0))
	if len(rest) != 1 || rest[0].ID != ts[2].ID {
		t.Fatalf("nach beenden: %+v", rest)
	}
}
