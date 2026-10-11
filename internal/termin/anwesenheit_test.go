package termin_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ruben-sch/teamtafel/internal/termin"
)

func (k *kader) vergangen(t *testing.T, typ string, beginn time.Time) string {
	t.Helper()
	tm, err := k.store.Anlegen(k.ctx, k.verein.ID, k.mannschaft.ID, "", termin.Daten{
		Typ: typ, Beginn: beginn, Ende: beginn.Add(90 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return tm.ID
}

func (k *kader) melden(t *testing.T, terminID, spielerID, status, grund string) {
	t.Helper()
	if err := k.store.Rueckmelden(k.ctx, k.verein.ID, terminID, spielerID, k.trainer.ID, status, grund, true); err != nil {
		t.Fatal(err)
	}
}

func (k *kader) anwesend(t *testing.T, terminID, spielerID string, ja bool) {
	t.Helper()
	if err := k.store.AnwesenheitSetzen(k.ctx, k.verein.ID, terminID, spielerID, k.trainer.ID, ja); err != nil {
		t.Fatal(err)
	}
}

func TestAnwesenheitZaehltVergangeneTermineJeTyp(t *testing.T) {
	k := mitKader(t)
	t1 := k.vergangen(t, termin.TypTraining, beiBerlin(2026, 9, 22, 17, 0))
	t2 := k.vergangen(t, termin.TypTraining, beiBerlin(2026, 9, 29, 17, 0))
	t3 := k.vergangen(t, termin.TypTraining, beiBerlin(2026, 10, 6, 17, 0))
	sp := k.vergangen(t, termin.TypSpiel, beiBerlin(2026, 10, 3, 10, 0))
	abgesagt := k.vergangen(t, termin.TypTraining, beiBerlin(2026, 10, 1, 17, 0))
	kommend := k.einzel(t) // 17.10., liegt nach jetzt

	k.melden(t, t1, k.kind, termin.Zu, "")
	k.melden(t, t2, k.kind, termin.Zu, "")
	k.melden(t, t3, k.kind, termin.Ab, termin.GrundKrank)
	k.melden(t, sp, k.kind, termin.Zu, "")
	k.melden(t, abgesagt, k.kind, termin.Zu, "")
	k.melden(t, kommend.ID, k.kind, termin.Zu, "")
	k.melden(t, t1, k.eigener, termin.Ab, "")
	// Trainer korrigiert: Kim war trotz Absage da und trotz Zusage beim Spiel nicht, Alex kam ohne Rückmeldung.
	k.anwesend(t, t3, k.kind, true)
	k.anwesend(t, sp, k.kind, false)
	k.anwesend(t, t2, k.erwachsen, true)
	if err := k.store.Absagen(k.ctx, k.verein.ID, abgesagt, k.trainer.ID); err != nil {
		t.Fatal(err)
	}

	as, err := k.store.Anwesenheit(k.ctx, k.verein.ID, k.mannschaft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(as) != 3 || as[0].Vorname != "Alex" || as[1].Vorname != "Kim" || as[2].Vorname != "Sam" {
		t.Fatalf("spieler: %+v", as)
	}
	pruefe := func(name string, got, want termin.Beteiligung) {
		t.Helper()
		if got != want {
			t.Errorf("%s: %+v, will %+v", name, got, want)
		}
	}
	kim := as[1]
	pruefe("kim training", kim.Training, termin.Beteiligung{Termine: 3, Anwesend: 3})
	pruefe("kim spiel", kim.Spiel, termin.Beteiligung{Termine: 1})
	pruefe("kim gesamt", kim.Gesamt, termin.Beteiligung{Termine: 4, Anwesend: 3})
	if q := kim.Gesamt.Quote(); q != 75 {
		t.Errorf("kim quote: %d", q)
	}
	pruefe("alex gesamt", as[0].Gesamt, termin.Beteiligung{Termine: 4, Anwesend: 1})
	pruefe("sam gesamt", as[2].Gesamt, termin.Beteiligung{Termine: 4})
}

func TestAnwesenheitIstVorbelegtAusRueckmeldung(t *testing.T) {
	k := mitKader(t)
	tm := k.vergangen(t, termin.TypTraining, beiBerlin(2026, 10, 6, 17, 0))
	k.melden(t, tm, k.kind, termin.Zu, "")
	k.melden(t, tm, k.eigener, termin.Ab, "")
	for id, want := range map[string]bool{k.kind: true, k.eigener: false, k.erwachsen: false} {
		if r := status(t, k, tm, id); r.Anwesend != want || r.AnwesendKorrigiert {
			t.Errorf("%s vorbelegt: %+v", r.Vorname, r)
		}
	}
	k.anwesend(t, tm, k.kind, false)
	if r := status(t, k, tm, k.kind); r.Anwesend || !r.AnwesendKorrigiert || r.Status != termin.Zu {
		t.Errorf("nach korrektur, zusage bleibt: %+v", r)
	}
}

func TestAnwesenheitSetzenNurNachBeginn(t *testing.T) {
	k := mitKader(t)
	kommend := k.einzel(t)
	if err := k.store.AnwesenheitSetzen(k.ctx, k.verein.ID, kommend.ID, k.kind, k.trainer.ID, true); !errors.Is(err, termin.ErrNichtBegonnen) {
		t.Errorf("kommender termin: %v", err)
	}
	abgesagt := k.vergangen(t, termin.TypTraining, beiBerlin(2026, 10, 6, 17, 0))
	if err := k.store.Absagen(k.ctx, k.verein.ID, abgesagt, k.trainer.ID); err != nil {
		t.Fatal(err)
	}
	if err := k.store.AnwesenheitSetzen(k.ctx, k.verein.ID, abgesagt, k.kind, k.trainer.ID, true); !errors.Is(err, termin.ErrAbgesagt) {
		t.Errorf("abgesagter termin: %v", err)
	}
	tm := k.vergangen(t, termin.TypTraining, beiBerlin(2026, 10, 7, 17, 0))
	if err := k.store.AnwesenheitSetzen(k.ctx, k.verein.ID, tm, k.trainer.ID, k.trainer.ID, true); !errors.Is(err, termin.ErrNichtBerechtigt) {
		t.Errorf("nicht im kader: %v", err)
	}
}

func TestAnwesenheitOhneVergangeneTermine(t *testing.T) {
	k := mitKader(t)
	k.einzel(t)
	as, err := k.store.Anwesenheit(k.ctx, k.verein.ID, k.mannschaft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(as) != 3 {
		t.Fatalf("spieler: %+v", as)
	}
	for _, a := range as {
		if a.Gesamt != (termin.Beteiligung{}) || a.Gesamt.Quote() != 0 {
			t.Errorf("%s: %+v", a.Vorname, a.Gesamt)
		}
	}
}
