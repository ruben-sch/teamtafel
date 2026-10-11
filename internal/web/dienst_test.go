package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ruben-sch/teamtafel/internal/termin"
)

func TestDiensteAmTermin(t *testing.T) {
	w := neueTerminWelt(t)
	ctx := context.Background()
	beginn := time.Now().In(termin.Zeitzone).AddDate(0, 0, 7)
	tm, err := w.termine.Anlegen(ctx, w.verein.ID, w.bambini.ID, w.trainerID, termin.Daten{
		Typ: termin.TypSpiel, Beginn: beginn, Ende: beginn.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	tPath := "/t/" + tm.ID
	dienst := func() termin.Dienst {
		t.Helper()
		ds, err := w.termine.Dienste(ctx, w.verein.ID, tm.ID)
		if err != nil || len(ds) != 1 {
			t.Fatalf("dienste = %+v, %v", ds, err)
		}
		return ds[0]
	}
	post := func(pfad string, form url.Values, c *http.Cookie, want int) string {
		t.Helper()
		rec := w.do(http.MethodPost, pfad, form, c)
		if rec.Code != want {
			t.Fatalf("POST %s: %d, want %d: %s", pfad, rec.Code, want, rec.Body.String())
		}
		return rec.Body.String()
	}

	// Anlegen nur durch Trainer und Vereinsadmin.
	kuchen := url.Values{"bezeichnung": {"Kuchen"}, "plaetze": {"1"}}
	post(tPath+"/dienste", kuchen, w.eltern, http.StatusForbidden)
	post(tPath+"/dienste", url.Values{"bezeichnung": {""}, "plaetze": {"1"}}, w.trainer, http.StatusBadRequest)
	post(tPath+"/dienste", kuchen, w.trainer, http.StatusSeeOther)
	d := dienst()
	dPath := tPath + "/dienste/" + d.ID

	adminCookie, admin := w.anmelden(fmt.Sprintf("admin-%d@example.org", time.Now().UnixNano()))
	if err := w.vereine.AdminHinzufuegen(ctx, w.verein.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	post(dPath, url.Values{"bezeichnung": {"Kuchen"}, "plaetze": {"2"}}, adminCookie, http.StatusSeeOther)
	post(dPath, url.Values{"bezeichnung": {"Kuchen"}, "plaetze": {"1"}}, w.eltern, http.StatusForbidden)
	post(dPath, url.Values{"bezeichnung": {"Kuchen"}, "plaetze": {"1"}}, w.trainer, http.StatusSeeOther)

	// Eltern sehen den Dienst und tragen sich ein; Fremde nicht.
	if body := w.do(http.MethodGet, tPath, nil, w.eltern).Body.String(); !strings.Contains(body, "Kuchen") ||
		!strings.Contains(body, dPath+"/eintragen") || strings.Contains(body, dPath+"/loeschen") {
		t.Fatalf("eltern-ansicht: %s", body)
	}
	post(dPath+"/eintragen", url.Values{}, w.fremd, http.StatusForbidden)
	post(dPath+"/eintragen", url.Values{"konto": {w.trainerID}}, w.eltern, http.StatusForbidden)
	post(dPath+"/eintragen", url.Values{}, w.eltern, http.StatusSeeOther)
	if body := w.do(http.MethodGet, tPath, nil, w.trainer).Body.String(); !strings.Contains(body, "Eltern von Ben") {
		t.Fatalf("trainer sieht eintrag nicht: %s", body)
	}
	if body := w.do(http.MethodGet, "/", nil, w.eltern).Body.String(); !strings.Contains(body, "Du: Kuchen") {
		t.Fatalf("startseite ohne eigenen dienst: %s", body)
	}
	if body := w.do(http.MethodGet, "/", nil, w.trainer).Body.String(); !strings.Contains(body, "Dienste: alle Plätze belegt") {
		t.Fatalf("startseite trainer: %s", body)
	}
	post(dPath+"/eintragen", url.Values{}, w.trainer, http.StatusConflict)

	// Trainer entfernt und teilt ein; Eltern dürfen andere nicht austragen.
	post(dPath+"/austragen", url.Values{"konto": {w.elternID}}, w.fremd, http.StatusForbidden)
	post(dPath+"/austragen", url.Values{"konto": {w.elternID}}, w.trainer, http.StatusSeeOther)
	if len(dienst().Eintraege) != 0 {
		t.Fatal("nach entfernen noch eingetragen")
	}
	if body := w.do(http.MethodGet, "/", nil, w.trainer).Body.String(); !strings.Contains(body, "Dienste: 1 Platz frei") {
		t.Fatalf("startseite trainer: %s", body)
	}
	post(dPath+"/eintragen", url.Values{"konto": {w.elternID}}, w.trainer, http.StatusSeeOther)
	if e := dienst().Eintraege; len(e) != 1 || e[0].KontoID != w.elternID || e[0].Selbst() {
		t.Fatalf("nach einteilen: %+v", e)
	}
	post(dPath+"/austragen", url.Values{"konto": {w.trainerID}}, w.eltern, http.StatusForbidden)
	post(dPath+"/austragen", url.Values{}, w.eltern, http.StatusSeeOther)

	post(dPath+"/loeschen", url.Values{}, w.eltern, http.StatusForbidden)
	post(dPath+"/loeschen", url.Values{}, w.trainer, http.StatusSeeOther)
	if ds, _ := w.termine.Dienste(ctx, w.verein.ID, tm.ID); len(ds) != 0 {
		t.Fatalf("nach löschen: %+v", ds)
	}
}
