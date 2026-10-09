package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ruben-sch/teamtafel/internal/dbtest"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/termin"
)

type terminWelt struct {
	*teamWelt
	termine *termin.Store
	trainer *http.Cookie
	eltern  *http.Cookie
	fremd   *http.Cookie
}

// neueTerminWelt: Mannschaft mit Trainer, einem Elternteil mit Kind im Kader und einem Fremden.
func neueTerminWelt(t *testing.T) *terminWelt {
	t.Helper()
	w := &terminWelt{teamWelt: neueTeamWelt(t), termine: termin.NewStore(dbtest.AppPool(t))}
	w.h = NewHandler(Options{
		DB: fakePinger{}, Vereine: w.vereine, Auth: w.auth, Team: w.team, Termine: w.termine, Mailer: w.mailer,
		BaseHost: "teamtafel.example", Scheme: "https", Version: "test",
	})
	ctx := context.Background()
	n := time.Now().UnixNano()
	var trainer, eltern = w.konto(fmt.Sprintf("t-%d@example.org", n)), w.konto(fmt.Sprintf("e-%d@example.org", n))
	w.trainer, w.eltern = trainer.cookie, eltern.cookie
	w.fremd, _ = w.anmelden(fmt.Sprintf("f-%d@example.org", n))
	if err := w.team.TrainerHinzufuegen(ctx, w.verein.ID, w.bambini.ID, trainer.id); err != nil {
		t.Fatal(err)
	}
	a, err := w.team.AnfrageStellen(ctx, w.verein.ID, w.bambini.ID, eltern.id,
		team.AnfrageDaten{Art: team.ArtKind, Vorname: "Ben", Nachname: "Ball", Jahrgang: 2020})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.team.Freigeben(ctx, w.verein.ID, a.ID, trainer.id, false); err != nil {
		t.Fatal(err)
	}
	return w
}

type angemeldetesKonto struct {
	cookie *http.Cookie
	id     string
}

func (w *terminWelt) konto(email string) angemeldetesKonto {
	c, k := w.anmelden(email)
	return angemeldetesKonto{c, k.ID}
}

var terminLink = regexp.MustCompile(`/t/([0-9a-f-]{36})`)

func naechsteWoche() string {
	return time.Now().In(termin.Zeitzone).AddDate(0, 0, 7).Format("2006-01-02")
}

func TestTrainerLegtEinzelterminAn(t *testing.T) {
	w := neueTerminWelt(t)
	m := "/m/" + w.bambini.ID
	form := url.Values{"typ": {"spiel"}, "titel": {"gegen SV Nachbar"}, "datum": {naechsteWoche()},
		"beginn": {"10:00"}, "ende": {"11:30"}, "treffzeit": {"09:15"}, "ort": {"Sportplatz"}}

	if rec := w.do(http.MethodGet, m+"/termine/neu", nil, w.eltern); rec.Code != http.StatusForbidden {
		t.Fatalf("eltern sehen formular: %d", rec.Code)
	}
	if rec := w.do(http.MethodPost, m+"/termine", form, w.eltern); rec.Code != http.StatusForbidden {
		t.Fatalf("eltern legen an: %d", rec.Code)
	}
	if rec := w.do(http.MethodGet, m+"/termine/neu", nil, w.trainer); rec.Code != http.StatusOK {
		t.Fatalf("formular: %d", rec.Code)
	}
	bad := url.Values{"typ": {"spiel"}, "datum": {naechsteWoche()}, "beginn": {"10:00"}, "ende": {"09:00"}}
	if rec := w.do(http.MethodPost, m+"/termine", bad, w.trainer); rec.Code != http.StatusBadRequest {
		t.Fatalf("ende vor beginn: %d", rec.Code)
	}
	rec := w.do(http.MethodPost, m+"/termine", form, w.trainer)
	if rec.Code != http.StatusSeeOther || !terminLink.MatchString(rec.Header().Get("Location")) {
		t.Fatalf("anlegen: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	tPath := rec.Header().Get("Location")

	// Team sieht den Termin auf Startseite und Detailseite, Fremde nicht.
	for name, c := range map[string]*http.Cookie{"trainer": w.trainer, "eltern": w.eltern} {
		if body := w.do(http.MethodGet, "/", nil, c).Body.String(); !strings.Contains(body, tPath) || !strings.Contains(body, "gegen SV Nachbar") {
			t.Errorf("%s: startseite ohne termin: %s", name, body)
		}
		rec := w.do(http.MethodGet, tPath, nil, c)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "09:15") || !strings.Contains(rec.Body.String(), "Sportplatz") {
			t.Errorf("%s: detail %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := w.do(http.MethodGet, tPath, nil, w.fremd); rec.Code != http.StatusForbidden {
		t.Fatalf("fremd sieht termin: %d", rec.Code)
	}
	if body := w.do(http.MethodGet, tPath, nil, w.eltern).Body.String(); strings.Contains(body, "/absagen") {
		t.Fatal("eltern sehen absagen-knopf")
	}

	// Ändern und absagen nur durch Trainer.
	form.Set("ort", "Kunstrasen")
	if rec := w.do(http.MethodPost, tPath, form, w.eltern); rec.Code != http.StatusForbidden {
		t.Fatalf("eltern ändern: %d", rec.Code)
	}
	if rec := w.do(http.MethodPost, tPath, form, w.trainer); rec.Code != http.StatusSeeOther {
		t.Fatalf("ändern: %d %s", rec.Code, rec.Body.String())
	}
	if rec := w.do(http.MethodPost, tPath+"/absagen", url.Values{}, w.eltern); rec.Code != http.StatusForbidden {
		t.Fatalf("eltern sagen ab: %d", rec.Code)
	}
	if rec := w.do(http.MethodPost, tPath+"/absagen", url.Values{}, w.trainer); rec.Code != http.StatusSeeOther {
		t.Fatalf("absagen: %d", rec.Code)
	}
	body := w.do(http.MethodGet, tPath, nil, w.eltern).Body.String()
	if !strings.Contains(body, "Kunstrasen") || !strings.Contains(body, "Abgesagt") {
		t.Fatalf("nach ändern und absagen: %s", body)
	}
}

func TestTrainerLegtSerieAnUndBeendetSie(t *testing.T) {
	w := neueTerminWelt(t)
	m := "/m/" + w.bambini.ID
	form := url.Values{"wochentag": {"2"}, "uhrzeit": {"18:00"}, "dauer": {"90"}, "treff": {"15"}, "frist": {"24"},
		"ort": {"Sportplatz"}, "gueltig_von": {time.Now().In(termin.Zeitzone).Format("2006-01-02")}}
	if rec := w.do(http.MethodPost, m+"/serien", form, w.eltern); rec.Code != http.StatusForbidden {
		t.Fatalf("eltern legen serie an: %d", rec.Code)
	}
	if rec := w.do(http.MethodPost, m+"/serien", url.Values{"wochentag": {"2"}, "uhrzeit": {"x"}, "dauer": {"90"}}, w.trainer); rec.Code != http.StatusBadRequest {
		t.Fatalf("ungültige serie: %d", rec.Code)
	}
	if rec := w.do(http.MethodPost, m+"/serien", form, w.trainer); rec.Code != http.StatusSeeOther {
		t.Fatalf("serie: %d %s", rec.Code, rec.Body.String())
	}
	body := w.do(http.MethodGet, m, nil, w.trainer).Body.String()
	if n := len(terminLink.FindAllString(body, -1)); n < 8 {
		t.Fatalf("%d termine auf der mannschaftsseite, erwartet mindestens 8: %s", n, body)
	}
	if !strings.Contains(body, "Dienstag") || !strings.Contains(body, "18:00") {
		t.Fatalf("serie nicht gelistet: %s", body)
	}
	serien, _ := w.termine.Serien(context.Background(), w.verein.ID, w.bambini.ID)
	if len(serien) != 1 {
		t.Fatalf("serien = %+v", serien)
	}
	if rec := w.do(http.MethodPost, "/serien/"+serien[0].ID+"/beenden", url.Values{}, w.eltern); rec.Code != http.StatusForbidden {
		t.Fatalf("eltern beenden: %d", rec.Code)
	}
	if rec := w.do(http.MethodPost, "/serien/"+serien[0].ID+"/beenden", url.Values{}, w.trainer); rec.Code != http.StatusSeeOther {
		t.Fatalf("beenden: %d", rec.Code)
	}
	if body := w.do(http.MethodGet, m, nil, w.trainer).Body.String(); strings.Contains(body, "Dienstag") {
		t.Fatalf("serie nach beenden noch da: %s", body)
	}
}

func TestVereinsadminDarfTermineAnlegen(t *testing.T) {
	w := neueTerminWelt(t)
	ctx := context.Background()
	adminCookie, admin := w.anmelden(fmt.Sprintf("admin-%d@example.org", time.Now().UnixNano()))
	if err := w.vereine.AdminHinzufuegen(ctx, w.verein.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"typ": {"training"}, "datum": {naechsteWoche()}, "beginn": {"17:00"}, "ende": {"18:30"}}
	if rec := w.do(http.MethodPost, "/m/"+w.bambini.ID+"/termine", form, adminCookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("admin legt termin an: %d", rec.Code)
	}
	if rec := w.do(http.MethodGet, "/m/"+w.bambini.ID, nil, adminCookie); rec.Code != http.StatusOK {
		t.Fatalf("admin auf mannschaftsseite: %d", rec.Code)
	}
}
