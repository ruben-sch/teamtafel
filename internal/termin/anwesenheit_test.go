package termin_test

import (
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
	kim := as[1]
	if want := (termin.Beteiligung{Termine: 3, Zu: 2, Ab: 1}); kim.Training != want {
		t.Errorf("kim training: %+v, will %+v", kim.Training, want)
	}
	if want := (termin.Beteiligung{Termine: 1, Zu: 1}); kim.Spiel != want {
		t.Errorf("kim spiel: %+v, will %+v", kim.Spiel, want)
	}
	if want := (termin.Beteiligung{Termine: 4, Zu: 3, Ab: 1}); kim.Gesamt != want {
		t.Errorf("kim gesamt: %+v, will %+v", kim.Gesamt, want)
	}
	if q := kim.Training.Quote(); q != 67 {
		t.Errorf("kim quote: %d", q)
	}
	alex := as[0]
	if alex.Gesamt.Termine != 4 || alex.Gesamt.Offen() != 4 || alex.Gesamt.Quote() != 0 {
		t.Errorf("alex ohne rückmeldung: %+v", alex.Gesamt)
	}
	if sam := as[2]; sam.Training.Ab != 1 || sam.Training.Offen() != 2 {
		t.Errorf("sam: %+v", sam.Training)
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
