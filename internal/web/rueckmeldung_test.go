package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ruben-sch/teamtafel/internal/termin"
)

func (w *terminWelt) termin(t *testing.T, frist *time.Time) termin.Termin {
	t.Helper()
	b := time.Now().In(termin.Zeitzone).AddDate(0, 0, 3).Truncate(time.Hour)
	tm, err := w.termine.Anlegen(context.Background(), w.verein.ID, w.bambini.ID, "", termin.Daten{
		Typ: termin.TypTraining, Beginn: b, Ende: b.Add(time.Hour), Frist: frist,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func (w *terminWelt) spielerID(t *testing.T) string {
	t.Helper()
	sp, err := w.team.Kader(context.Background(), w.verein.ID, w.bambini.ID)
	if err != nil || len(sp) != 1 {
		t.Fatalf("kader = %+v %v", sp, err)
	}
	return sp[0].ID
}

func TestElternSagenVonDerStartseiteZu(t *testing.T) {
	w := neueTerminWelt(t)
	tm := w.termin(t, nil)
	sp := w.spielerID(t)
	ziel := "/t/" + tm.ID + "/rueckmeldung/" + sp

	body := w.do(http.MethodGet, "/", nil, w.eltern).Body.String()
	if !strings.Contains(body, `action="`+ziel+`"`) {
		t.Fatalf("startseite ohne zu/ab-knöpfe: %s", body)
	}
	rec := w.do(http.MethodPost, ziel, url.Values{"status": {"zu"}, "zurueck": {"/"}}, w.eltern)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("zusage: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if body := w.do(http.MethodGet, "/", nil, w.eltern).Body.String(); !strings.Contains(body, "Ben: zugesagt") {
		t.Fatalf("status fehlt auf der startseite: %s", body)
	}

	// Fremde und offene Weiterleitungen.
	if rec := w.do(http.MethodPost, ziel, url.Values{"status": {"ab"}}, w.fremd); rec.Code != http.StatusForbidden {
		t.Fatalf("fremd: %d", rec.Code)
	}
	if rec := w.do(http.MethodPost, ziel, url.Values{"status": {"zu"}, "zurueck": {"//evil.example"}}, w.eltern); rec.Header().Get("Location") != "/" {
		t.Fatalf("zurueck auf fremde seite: %q", rec.Header().Get("Location"))
	}
	if rec := w.do(http.MethodPost, ziel, url.Values{"status": {"vielleicht"}}, w.eltern); rec.Code != http.StatusBadRequest {
		t.Fatalf("ungültiger status: %d", rec.Code)
	}
}

func TestGruendeSiehtNurDerTrainer(t *testing.T) {
	w := neueTerminWelt(t)
	tm := w.termin(t, nil)
	sp := w.spielerID(t)
	ziel := "/t/" + tm.ID + "/rueckmeldung/" + sp
	if rec := w.do(http.MethodPost, ziel, url.Values{"status": {"ab"}, "grund": {"krank"}}, w.eltern); rec.Code != http.StatusSeeOther {
		t.Fatalf("absage: %d %s", rec.Code, rec.Body.String())
	}
	if body := w.do(http.MethodGet, "/t/"+tm.ID, nil, w.trainer).Body.String(); !strings.Contains(body, "Ab (krank)") {
		t.Fatalf("trainer sieht grund nicht: %s", body)
	}
	body := w.do(http.MethodGet, "/t/"+tm.ID, nil, w.eltern).Body.String()
	if strings.Contains(body, "Ab (krank)") || !strings.Contains(body, "Ben") {
		t.Fatalf("eltern-ansicht: %s", body)
	}
	// Das Detailformular schickt den gewählten Grund auch beim Klick auf Zu mit.
	if rec := w.do(http.MethodPost, ziel, url.Values{"status": {"zu"}, "grund": {"krank"}}, w.eltern); rec.Code != http.StatusSeeOther {
		t.Fatalf("zusage mit vorbelegtem grund: %d", rec.Code)
	}
	_ = w.do(http.MethodPost, ziel, url.Values{"status": {"ab"}, "grund": {"krank"}}, w.eltern)
	// Mannschaftsseite zählt.
	if body := w.do(http.MethodGet, "/m/"+w.bambini.ID, nil, w.trainer).Body.String(); !strings.Contains(body, "0 zu · 1 ab · 0 offen") {
		t.Fatalf("zähler fehlen: %s", body)
	}
}

func TestNachDerFristNurTrainer(t *testing.T) {
	w := neueTerminWelt(t)
	frist := time.Now().Add(-time.Minute)
	tm := w.termin(t, &frist)
	sp := w.spielerID(t)
	ziel := "/t/" + tm.ID + "/rueckmeldung/" + sp
	if rec := w.do(http.MethodPost, ziel, url.Values{"status": {"ab"}}, w.eltern); rec.Code != http.StatusConflict {
		t.Fatalf("eltern nach frist: %d", rec.Code)
	}
	if body := w.do(http.MethodGet, "/t/"+tm.ID, nil, w.eltern).Body.String(); !strings.Contains(body, "Frist ist vorbei") {
		t.Fatalf("kein hinweis auf frist: %s", body)
	}
	if rec := w.do(http.MethodPost, ziel, url.Values{"status": {"ab"}, "grund": {"urlaub"}}, w.trainer); rec.Code != http.StatusSeeOther {
		t.Fatalf("trainer nach frist: %d", rec.Code)
	}
}
