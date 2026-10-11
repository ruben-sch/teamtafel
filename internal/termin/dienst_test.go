package termin_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ruben-sch/teamtafel/internal/termin"
)

func (k *kader) dienst(t *testing.T, terminID, bezeichnung string, plaetze int) termin.Dienst {
	t.Helper()
	d, err := k.store.DienstAnlegen(k.ctx, k.verein.ID, terminID, bezeichnung, plaetze)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func dienste(t *testing.T, k *kader, terminID string) []termin.Dienst {
	t.Helper()
	ds, err := k.store.Dienste(k.ctx, k.verein.ID, terminID)
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func namen(d termin.Dienst) []string {
	var out []string
	for _, e := range d.Eintraege {
		out = append(out, e.Name)
	}
	return out
}

func TestDienstAnlegenAendernLoeschen(t *testing.T) {
	k := mitKader(t)
	tm := k.einzel(t)
	for _, f := range []struct {
		bez     string
		plaetze int
	}{{"", 1}, {"   ", 1}, {"Kuchen", 0}, {"Kuchen", 21}} {
		if _, err := k.store.DienstAnlegen(k.ctx, k.verein.ID, tm.ID, f.bez, f.plaetze); !errors.Is(err, termin.ErrUngueltig) {
			t.Errorf("%q/%d: err = %v", f.bez, f.plaetze, err)
		}
	}
	d := k.dienst(t, tm.ID, " Kuchen ", 2)
	k.dienst(t, tm.ID, "Kasse", 1)
	ds := dienste(t, k, tm.ID)
	if len(ds) != 2 || ds[0].Bezeichnung != "Kuchen" || ds[0].Plaetze != 2 || ds[0].Frei() != 2 || ds[1].Bezeichnung != "Kasse" {
		t.Fatalf("dienste = %+v", ds)
	}

	if err := k.store.Eintragen(k.ctx, k.verein.ID, tm.ID, d.ID, k.eltern.ID, k.eltern.ID); err != nil {
		t.Fatal(err)
	}
	if err := k.store.Eintragen(k.ctx, k.verein.ID, tm.ID, d.ID, k.selbst.ID, k.selbst.ID); err != nil {
		t.Fatal(err)
	}
	// Weniger Plätze als Einträge geht nicht.
	if err := k.store.DienstAendern(k.ctx, k.verein.ID, tm.ID, d.ID, "Kuchen", 1); !errors.Is(err, termin.ErrUngueltig) {
		t.Errorf("unter einträge: err = %v", err)
	}
	if err := k.store.DienstAendern(k.ctx, k.verein.ID, tm.ID, d.ID, "Kuchen und Muffins", 3); err != nil {
		t.Fatal(err)
	}
	if ds := dienste(t, k, tm.ID); ds[0].Bezeichnung != "Kuchen und Muffins" || ds[0].Plaetze != 3 || ds[0].Frei() != 1 {
		t.Fatalf("nach ändern: %+v", ds[0])
	}

	// Der Dienst muss zum Termin passen.
	anderer := k.einzel(t)
	if err := k.store.DienstLoeschen(k.ctx, k.verein.ID, anderer.ID, d.ID); !errors.Is(err, termin.ErrNotFound) {
		t.Errorf("fremder termin: err = %v", err)
	}
	if err := k.store.DienstLoeschen(k.ctx, k.verein.ID, tm.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if ds := dienste(t, k, tm.ID); len(ds) != 1 || ds[0].Bezeichnung != "Kasse" {
		t.Fatalf("nach löschen: %+v", ds)
	}
}

func TestEintragenNurImTeamUndBisZumBeginn(t *testing.T) {
	k := mitKader(t)
	tm := k.einzel(t)
	d := k.dienst(t, tm.ID, "Kuchen", 2)
	ein := func(konto string) error {
		return k.store.Eintragen(k.ctx, k.verein.ID, tm.ID, d.ID, konto, konto)
	}
	if err := ein(k.fremd.ID); !errors.Is(err, termin.ErrNichtBerechtigt) {
		t.Errorf("fremd: err = %v", err)
	}
	if err := ein(k.eltern.ID); err != nil {
		t.Fatal(err)
	}
	// Doppelt eintragen ändert nichts.
	if err := ein(k.eltern.ID); err != nil {
		t.Fatal(err)
	}
	if err := ein(k.selbst.ID); err != nil {
		t.Fatal(err)
	}
	if err := ein(k.trainer.ID); !errors.Is(err, termin.ErrVoll) {
		t.Errorf("voll: err = %v", err)
	}
	ds := dienste(t, k, tm.ID)
	// Eltern erscheinen mit den Vornamen ihrer Kinder, Spieler mit eigenem Vornamen.
	if got, want := namen(ds[0]), []string{"Eltern von Alex und Kim", "Sam"}; !slices.Equal(got, want) {
		t.Errorf("namen = %q, want %q", got, want)
	}
	if !ds[0].Eintraege[0].Selbst() || ds[0].Frei() != 0 {
		t.Errorf("eintrag = %+v", ds[0])
	}

	if err := k.store.Austragen(k.ctx, k.verein.ID, tm.ID, d.ID, k.selbst.ID, k.selbst.ID); err != nil {
		t.Fatal(err)
	}
	if err := ein(k.trainer.ID); err != nil {
		t.Fatal(err)
	}
	if got := namen(dienste(t, k, tm.ID)[0]); !slices.Equal(got, []string{"Eltern von Alex und Kim", "Trainer"}) {
		t.Errorf("mit trainer: %q", got)
	}

	k.jetzt = tm.Beginn
	if err := ein(k.selbst.ID); !errors.Is(err, termin.ErrFristVorbei) {
		t.Errorf("nach beginn: err = %v", err)
	}
	k.jetzt = beiBerlin(2026, 10, 9, 12, 0)
	if err := k.store.Absagen(k.ctx, k.verein.ID, tm.ID, k.trainer.ID); err != nil {
		t.Fatal(err)
	}
	if err := ein(k.selbst.ID); !errors.Is(err, termin.ErrAbgesagt) {
		t.Errorf("abgesagt: err = %v", err)
	}
}

func TestEinteilenBenachrichtigtDiePerson(t *testing.T) {
	k := mitKader(t)
	tm := k.einzel(t)
	d := k.dienst(t, tm.ID, "Trikotwäsche", 1)
	if err := k.store.Eintragen(k.ctx, k.verein.ID, tm.ID, d.ID, k.fremd.ID, k.trainer.ID); !errors.Is(err, termin.ErrNichtBerechtigt) {
		t.Errorf("fremd einteilen: err = %v", err)
	}
	if err := k.store.Eintragen(k.ctx, k.verein.ID, tm.ID, d.ID, k.eltern.ID, k.trainer.ID); err != nil {
		t.Fatal(err)
	}
	b := betreffe(t, k, k.eltern, "Eingeteilt")
	if want := "Eingeteilt: Trikotwäsche, Spiel gegen SV Nachbar am Sa 17.10. um 10:00"; !slices.Equal(b, []string{want}) {
		t.Errorf("eltern: %q, want %q", b, want)
	}
	if e := dienste(t, k, tm.ID)[0].Eintraege[0]; e.Selbst() {
		t.Errorf("eintrag gilt als selbst: %+v", e)
	}
	// Selbst eintragen benachrichtigt niemanden.
	if anzahl(t, k, k.trainer, "Eingeteilt") != 0 {
		t.Error("trainer benachrichtigt")
	}
	// Eingeteilte tragen sich selbst wieder aus, mehr als 2 Tage vorher ohne Meldung an die Trainer.
	if err := k.store.Austragen(k.ctx, k.verein.ID, tm.ID, d.ID, k.eltern.ID, k.eltern.ID); err != nil {
		t.Fatal(err)
	}
	if anzahl(t, k, k.trainer, "ausgetragen") != 0 {
		t.Error("trainer bei frühem austragen benachrichtigt")
	}
	// Austragen anderer nur durch Trainer; das prüft der Aufrufer, der Store kennt keine Rollen.
	if ds := dienste(t, k, tm.ID); len(ds[0].Eintraege) != 0 {
		t.Errorf("nach austragen: %+v", ds[0])
	}
}

func TestAustragenKurzVorherMeldetTrainer(t *testing.T) {
	k := mitKader(t)
	tm := k.einzel(t)
	d := k.dienst(t, tm.ID, "Kuchen", 1)
	if err := k.store.Eintragen(k.ctx, k.verein.ID, tm.ID, d.ID, k.selbst.ID, k.selbst.ID); err != nil {
		t.Fatal(err)
	}
	k.jetzt = tm.Beginn.Add(-47 * time.Hour)
	if err := k.store.Austragen(k.ctx, k.verein.ID, tm.ID, d.ID, k.selbst.ID, k.selbst.ID); err != nil {
		t.Fatal(err)
	}
	b := betreffe(t, k, k.trainer, "ausgetragen")
	if want := "E1: Sam hat sich ausgetragen: Kuchen, Spiel gegen SV Nachbar am Sa 17.10. um 10:00"; !slices.Equal(b, []string{want}) {
		t.Errorf("trainer: %q, want %q", b, want)
	}
	// Nicht eingetragen: nichts passiert.
	if err := k.store.Austragen(k.ctx, k.verein.ID, tm.ID, d.ID, k.selbst.ID, k.selbst.ID); err != nil {
		t.Fatal(err)
	}
	if anzahl(t, k, k.trainer, "ausgetragen") != 1 {
		t.Error("doppelte meldung")
	}
	// Entfernt der Trainer jemanden, meldet er das nicht sich selbst.
	if err := k.store.Eintragen(k.ctx, k.verein.ID, tm.ID, d.ID, k.eltern.ID, k.eltern.ID); err != nil {
		t.Fatal(err)
	}
	if err := k.store.Austragen(k.ctx, k.verein.ID, tm.ID, d.ID, k.eltern.ID, k.trainer.ID); err != nil {
		t.Fatal(err)
	}
	if anzahl(t, k, k.trainer, "ausgetragen") != 1 {
		t.Error("trainer über eigenes entfernen benachrichtigt")
	}
}

func TestDienstHinweisZweiTageVorher(t *testing.T) {
	k := mitKader(t)
	tm := k.einzel(t)
	d := k.dienst(t, tm.ID, "Kuchen", 2)
	k.dienst(t, tm.ID, "Kasse", 1)
	if err := k.store.Eintragen(k.ctx, k.verein.ID, tm.ID, d.ID, k.selbst.ID, k.selbst.ID); err != nil {
		t.Fatal(err)
	}
	erinnern := func() {
		t.Helper()
		if err := k.store.DiensteErinnern(k.ctx, k.verein.ID); err != nil {
			t.Fatal(err)
		}
	}
	erinnern()
	if anzahl(t, k, k.eltern, "Plätze frei") != 0 {
		t.Fatal("hinweis zu früh")
	}
	k.jetzt = tm.Beginn.Add(-48 * time.Hour)
	erinnern()
	erinnern()
	b := betreffe(t, k, k.eltern, "Plätze frei")
	if want := "E1: Dienste: 2 Plätze frei, Spiel gegen SV Nachbar am Sa 17.10. um 10:00"; !slices.Equal(b, []string{want}) {
		t.Errorf("eltern: %q, want %q", b, want)
	}
	// Wer schon eingetragen ist, bekommt keinen Hinweis; Trainer gehören zum Team.
	if anzahl(t, k, k.selbst, "Plätze frei") != 0 || anzahl(t, k, k.trainer, "Plätze frei") != 1 || anzahl(t, k, k.fremd, "") != 0 {
		t.Error("empfänger falsch")
	}

	// Alle Plätze belegt: kein Hinweis.
	voll := k.einzel(t)
	v := k.dienst(t, voll.ID, "Kasse", 1)
	if err := k.store.Eintragen(k.ctx, k.verein.ID, voll.ID, v.ID, k.selbst.ID, k.selbst.ID); err != nil {
		t.Fatal(err)
	}
	erinnern()
	if n := anzahl(t, k, k.trainer, "Plätze frei"); n != 1 {
		t.Errorf("hinweise an trainer: %d", n)
	}
}

func TestDienstErinnerungAmVortag(t *testing.T) {
	k := mitKader(t)
	tm := k.einzel(t)
	kuchen := k.dienst(t, tm.ID, "Kuchen", 2)
	kasse := k.dienst(t, tm.ID, "Kasse", 1)
	for _, d := range []termin.Dienst{kuchen, kasse} {
		if err := k.store.Eintragen(k.ctx, k.verein.ID, tm.ID, d.ID, k.eltern.ID, k.eltern.ID); err != nil {
			t.Fatal(err)
		}
	}
	erinnern := func() {
		t.Helper()
		if err := k.store.DiensteErinnern(k.ctx, k.verein.ID); err != nil {
			t.Fatal(err)
		}
	}
	k.jetzt = beiBerlin(2026, 10, 16, 17, 55)
	erinnern()
	if anzahl(t, k, k.eltern, "Morgen") != 0 {
		t.Fatal("erinnerung vor 18 uhr")
	}
	k.jetzt = beiBerlin(2026, 10, 16, 18, 0)
	erinnern()
	erinnern()
	// Eine Nachricht je Termin mit allen eigenen Diensten.
	b := betreffe(t, k, k.eltern, "Morgen")
	if want := "Morgen: Kasse, Kuchen, Spiel gegen SV Nachbar am Sa 17.10. um 10:00"; !slices.Equal(b, []string{want}) {
		t.Errorf("eltern: %q, want %q", b, want)
	}

	// Wer sich nach 18 Uhr am Vortag einträgt, bekommt keine Erinnerung mehr.
	if err := k.store.Eintragen(k.ctx, k.verein.ID, tm.ID, kuchen.ID, k.selbst.ID, k.selbst.ID); err != nil {
		t.Fatal(err)
	}
	erinnern()
	if anzahl(t, k, k.selbst, "Morgen") != 0 {
		t.Error("späte erinnerung")
	}
}

func TestDienstUebersicht(t *testing.T) {
	k := mitKader(t)
	tm := k.einzel(t)
	ohne := k.einzel(t)
	kuchen := k.dienst(t, tm.ID, "Kuchen", 2)
	k.dienst(t, tm.ID, "Kasse", 1)
	if err := k.store.Eintragen(k.ctx, k.verein.ID, tm.ID, kuchen.ID, k.eltern.ID, k.eltern.ID); err != nil {
		t.Fatal(err)
	}
	u, err := k.store.DienstUebersicht(k.ctx, k.verein.ID, k.eltern.ID, []string{tm.ID, ohne.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := u[tm.ID]; got.Frei != 2 || !slices.Equal(got.Meine, []string{"Kuchen"}) {
		t.Errorf("termin: %+v", got)
	}
	if _, ok := u[ohne.ID]; ok {
		t.Errorf("termin ohne dienste: %+v", u[ohne.ID])
	}
}
