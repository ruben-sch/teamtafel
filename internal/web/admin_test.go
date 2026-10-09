package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/dbtest"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

type adminWelt struct {
	t      *testing.T
	h      http.Handler
	auth   *auth.Store
	verein *verein.Store
	team   *team.Store
	chef   string // Super-Admin
}

func neueAdminWelt(t *testing.T) *adminWelt {
	t.Helper()
	pool := dbtest.AppPool(t)
	w := &adminWelt{t: t, auth: auth.NewStore(pool), verein: verein.NewStore(pool), team: team.NewStore(pool),
		chef: fmt.Sprintf("chef-%d@example.org", time.Now().UnixNano())}
	w.h = NewHandler(Options{
		DB: fakePinger{}, Vereine: w.verein, Auth: w.auth, Team: w.team, Mailer: &fakeMailer{},
		Superadmins: []string{" " + strings.ToUpper(w.chef) + " "},
		BaseHost:    "teamtafel.example", Scheme: "https", Version: "test",
	})
	return w
}

func (w *adminWelt) anmelden(host, email string) *http.Cookie {
	w.t.Helper()
	tok, err := w.auth.LinkAnfordern(context.Background(), email)
	if err != nil {
		w.t.Fatal(err)
	}
	sess, _, err := w.auth.Einloesen(context.Background(), tok)
	if err != nil {
		w.t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: sess}
}

func (w *adminWelt) do(host, method, target string, form url.Values, c *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	req.Host = host
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, req)
	return rec
}

func TestSuperadminLegtVereinMitAdminAn(t *testing.T) {
	w := neueAdminWelt(t)
	const haupt = "teamtafel.example"
	chef := w.anmelden(haupt, w.chef)
	fremd := w.anmelden(haupt, fmt.Sprintf("fremd-%d@example.org", time.Now().UnixNano()))

	if rec := w.do(haupt, http.MethodGet, "/admin", nil, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("ohne login: %d", rec.Code)
	}
	if rec := w.do(haupt, http.MethodGet, "/admin", nil, fremd); rec.Code != http.StatusForbidden {
		t.Fatalf("fremd: %d", rec.Code)
	}
	if body := w.do(haupt, http.MethodGet, "/", nil, chef).Body.String(); !strings.Contains(body, `href="/admin"`) {
		t.Fatalf("startseite ohne admin-link: %s", body)
	}

	slug := fmt.Sprintf("sv%d", time.Now().UnixNano())
	vorstand := fmt.Sprintf("vorstand-%d@example.org", time.Now().UnixNano())
	form := url.Values{"slug": {slug}, "name": {"SV Neu"}, "admin": {vorstand}}
	if rec := w.do(haupt, http.MethodPost, "/admin/vereine", form, fremd); rec.Code != http.StatusForbidden {
		t.Fatalf("fremd legt an: %d", rec.Code)
	}
	if rec := w.do(haupt, http.MethodPost, "/admin/vereine", form, chef); rec.Code != http.StatusSeeOther {
		t.Fatalf("anlegen: %d %s", rec.Code, rec.Body.String())
	}
	body := w.do(haupt, http.MethodGet, "/admin", nil, chef).Body.String()
	if !strings.Contains(body, "SV Neu") || !strings.Contains(body, "https://"+slug+".teamtafel.example") {
		t.Fatalf("verein fehlt in der liste: %s", body)
	}

	// Doppelter Slug und ungültige Angaben zeigen einen Fehler.
	if rec := w.do(haupt, http.MethodPost, "/admin/vereine", form, chef); rec.Code != http.StatusBadRequest {
		t.Fatalf("doppelt: %d", rec.Code)
	}
	if rec := w.do(haupt, http.MethodPost, "/admin/vereine", url.Values{"slug": {"Admin"}, "name": {"x"}, "admin": {vorstand}}, chef); rec.Code != http.StatusBadRequest {
		t.Fatalf("ungültiger slug: %d", rec.Code)
	}

	v, err := w.verein.BySlug(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := w.auth.KontoFuer(context.Background(), vorstand)
	if ok, _ := w.verein.IstAdmin(context.Background(), v.ID, k.ID); !ok {
		t.Fatal("vorstand ist kein vereinsadmin")
	}
}

func TestVereinsadminVerwaltetMannschaftenUndTrainer(t *testing.T) {
	w := neueAdminWelt(t)
	ctx := context.Background()
	v, err := w.verein.Anlegen(ctx, fmt.Sprintf("fc%d", time.Now().UnixNano()), "FC Verwaltung")
	if err != nil {
		t.Fatal(err)
	}
	host := v.Slug + ".teamtafel.example"
	vorstandEmail := fmt.Sprintf("vorstand-%d@example.org", time.Now().UnixNano())
	vorstand, _ := w.auth.KontoFuer(ctx, vorstandEmail)
	if err := w.verein.AdminHinzufuegen(ctx, v.ID, vorstand.ID); err != nil {
		t.Fatal(err)
	}
	admin := w.anmelden(host, vorstandEmail)
	eltern := w.anmelden(host, fmt.Sprintf("eltern-%d@example.org", time.Now().UnixNano()))

	if rec := w.do(host, http.MethodGet, "/admin", nil, eltern); rec.Code != http.StatusForbidden {
		t.Fatalf("eltern: %d", rec.Code)
	}
	if rec := w.do(host, http.MethodPost, "/admin/mannschaften", url.Values{"saison": {"2026/27"}, "name": {"F1"}}, eltern); rec.Code != http.StatusForbidden {
		t.Fatalf("eltern legen an: %d", rec.Code)
	}
	if body := w.do(host, http.MethodGet, "/", nil, admin).Body.String(); !strings.Contains(body, `href="/admin"`) {
		t.Fatalf("startseite ohne admin-link: %s", body)
	}

	// Mannschaft anlegen, ungültige Saison wird abgelehnt.
	if rec := w.do(host, http.MethodPost, "/admin/mannschaften", url.Values{"saison": {"2026"}, "name": {"F1"}}, admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("ungültige saison: %d", rec.Code)
	}
	if rec := w.do(host, http.MethodPost, "/admin/mannschaften", url.Values{"saison": {"2026/27"}, "name": {"F1"}}, admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("mannschaft: %d %s", rec.Code, rec.Body.String())
	}
	ms, _ := w.verein.Mannschaften(ctx, v.ID)
	if len(ms) != 1 || ms[0].Name != "F1" {
		t.Fatalf("mannschaften = %+v", ms)
	}

	// Trainer eintragen, auch bevor er sich je angemeldet hat.
	trainerEmail := fmt.Sprintf("trainer-%d@example.org", time.Now().UnixNano())
	if rec := w.do(host, http.MethodPost, "/admin/mannschaften/"+ms[0].ID+"/trainer", url.Values{"email": {trainerEmail}}, admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("trainer: %d %s", rec.Code, rec.Body.String())
	}
	if body := w.do(host, http.MethodGet, "/admin", nil, admin).Body.String(); !strings.Contains(body, trainerEmail) {
		t.Fatalf("trainer fehlt: %s", body)
	}
	trainer := w.anmelden(host, trainerEmail)
	if rec := w.do(host, http.MethodGet, "/m/"+ms[0].ID, nil, trainer); rec.Code != http.StatusOK {
		t.Fatalf("trainer sieht mannschaft nicht: %d", rec.Code)
	}

	// Weiteren Vereinsadmin eintragen.
	zweit := fmt.Sprintf("zweit-%d@example.org", time.Now().UnixNano())
	if rec := w.do(host, http.MethodPost, "/admin/admins", url.Values{"email": {zweit}}, admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("admin eintragen: %d", rec.Code)
	}
	if body := w.do(host, http.MethodGet, "/admin", nil, admin).Body.String(); !strings.Contains(body, zweit) {
		t.Fatalf("zweiter admin fehlt: %s", body)
	}

	// Trainer entfernen.
	tk, _ := w.auth.KontoFuer(ctx, trainerEmail)
	if rec := w.do(host, http.MethodPost, "/admin/mannschaften/"+ms[0].ID+"/trainer/"+tk.ID+"/entfernen", url.Values{}, admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("entfernen: %d", rec.Code)
	}
	if rec := w.do(host, http.MethodGet, "/m/"+ms[0].ID, nil, trainer); rec.Code != http.StatusForbidden {
		t.Fatalf("entfernter trainer: %d", rec.Code)
	}
}

func TestSuperadminDarfInJedenVerein(t *testing.T) {
	w := neueAdminWelt(t)
	v, _ := w.verein.Anlegen(context.Background(), fmt.Sprintf("fc%d", time.Now().UnixNano()), "FC Fremd")
	host := v.Slug + ".teamtafel.example"
	if rec := w.do(host, http.MethodGet, "/admin", nil, w.anmelden(host, w.chef)); rec.Code != http.StatusOK {
		t.Fatalf("superadmin im verein: %d", rec.Code)
	}
}
