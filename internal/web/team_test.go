package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/dbtest"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

type teamWelt struct {
	t       *testing.T
	h       http.Handler
	host    string
	auth    *auth.Store
	team    *team.Store
	verein  verein.Verein
	vereine *verein.Store
	bambini verein.Mannschaft
	mailer  *fakeMailer
}

func neueTeamWelt(t *testing.T) *teamWelt {
	t.Helper()
	pool := dbtest.AppPool(t)
	ctx := context.Background()
	vs := verein.NewStore(pool)
	v, err := vs.Anlegen(ctx, fmt.Sprintf("fc-%d", time.Now().UnixNano()), "FC Test")
	if err != nil {
		t.Fatal(err)
	}
	m, err := vs.MannschaftAnlegen(ctx, v.ID, "2026/27", "Bambini")
	if err != nil {
		t.Fatal(err)
	}
	w := &teamWelt{t: t, host: v.Slug + ".teamtafel.example", auth: auth.NewStore(pool), team: team.NewStore(pool),
		verein: v, vereine: vs, bambini: m, mailer: &fakeMailer{}}
	w.h = NewHandler(Options{
		DB: fakePinger{}, Vereine: vs, Auth: w.auth, Team: w.team, Mailer: w.mailer,
		BaseHost: "teamtafel.example", Scheme: "https", Version: "test",
	})
	return w
}

// anmelden liefert ein Session-Cookie für die Adresse.
func (w *teamWelt) anmelden(email string) (*http.Cookie, auth.Konto) {
	w.t.Helper()
	ctx := context.Background()
	tok, err := w.auth.LinkAnfordern(ctx, email)
	if err != nil {
		w.t.Fatal(err)
	}
	sess, k, err := w.auth.Einloesen(ctx, tok)
	if err != nil {
		w.t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: sess}, k
}

func (w *teamWelt) do(method, target string, form url.Values, c *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	req.Host = w.host
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

var joinPattern = regexp.MustCompile(`https://[^/"]+(/join/[A-Za-z0-9_-]+)`)

func TestBeitrittUeberTeamLinkMitFreigabe(t *testing.T) {
	w := neueTeamWelt(t)
	trainerCookie, trainer := w.anmelden(fmt.Sprintf("trainer-%d@example.org", time.Now().UnixNano()))
	if err := w.team.TrainerHinzufuegen(context.Background(), w.verein.ID, w.bambini.ID, trainer.ID); err != nil {
		t.Fatal(err)
	}
	elternCookie, _ := w.anmelden(fmt.Sprintf("eltern-%d@example.org", time.Now().UnixNano()))

	// Startseite des Trainers verlinkt seine Mannschaft.
	if body := w.do(http.MethodGet, "/", nil, trainerCookie).Body.String(); !strings.Contains(body, "/m/"+w.bambini.ID) {
		t.Fatalf("startseite ohne link zur mannschaft: %s", body)
	}

	// Nur Trainer sehen die Mannschaftsseite.
	if rec := w.do(http.MethodGet, "/m/"+w.bambini.ID, nil, elternCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("eltern auf /m: %d", rec.Code)
	}
	if rec := w.do(http.MethodPost, "/m/"+w.bambini.ID+"/einladung", url.Values{}, elternCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("eltern erneuern link: %d", rec.Code)
	}

	// Trainer erzeugt den Team-Link mit QR-Code.
	rec := w.do(http.MethodPost, "/m/"+w.bambini.ID+"/einladung", url.Values{}, trainerCookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<svg") {
		t.Fatalf("einladung: %d %s", rec.Code, rec.Body.String())
	}
	m := joinPattern.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatal("kein team-link auf der seite")
	}
	joinPath := m[1]

	// Ohne Login geht es zur Anmeldung, danach zurück zum Formular.
	rec = w.do(http.MethodGet, joinPath, nil, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?weiter="+url.QueryEscape(joinPath) {
		t.Fatalf("join ohne login: %d %q", rec.Code, rec.Header().Get("Location"))
	}

	rec = w.do(http.MethodGet, joinPath, nil, elternCookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Bambini") {
		t.Fatalf("join-formular: %d", rec.Code)
	}
	rec = w.do(http.MethodPost, joinPath, url.Values{
		"art": {"kind"}, "vorname": {"Mia"}, "nachname": {"Muster"}, "jahrgang": {"2020"},
	}, elternCookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "freigeben") {
		t.Fatalf("anfrage stellen: %d %s", rec.Code, rec.Body.String())
	}

	// Trainer sieht die Anfrage und gibt frei.
	body := w.do(http.MethodGet, "/m/"+w.bambini.ID, nil, trainerCookie).Body.String()
	if !strings.Contains(body, "Mia Muster") {
		t.Fatalf("anfrage nicht sichtbar: %s", body)
	}
	anfragen, _ := w.team.OffeneAnfragen(context.Background(), w.verein.ID, w.bambini.ID)
	if len(anfragen) != 1 {
		t.Fatalf("anfragen = %+v", anfragen)
	}
	if rec := w.do(http.MethodPost, "/anfragen/"+anfragen[0].ID+"/freigeben", url.Values{}, elternCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("eltern geben frei: %d", rec.Code)
	}
	rec = w.do(http.MethodPost, "/anfragen/"+anfragen[0].ID+"/freigeben", url.Values{}, trainerCookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("freigeben: %d %s", rec.Code, rec.Body.String())
	}

	// Kind steht im Kader und erscheint bei den Eltern.
	if body := w.do(http.MethodGet, "/m/"+w.bambini.ID, nil, trainerCookie).Body.String(); !strings.Contains(body, "Muster") || strings.Contains(body, "freigeben</button>") {
		t.Fatalf("kader nach freigabe: %s", body)
	}
	if body := w.do(http.MethodGet, "/", nil, elternCookie).Body.String(); !strings.Contains(body, "Mia") {
		t.Fatalf("eltern sehen ihr kind nicht: %s", body)
	}
}

func TestUngueltigerTeamLink(t *testing.T) {
	w := neueTeamWelt(t)
	c, _ := w.anmelden(fmt.Sprintf("x-%d@example.org", time.Now().UnixNano()))
	if rec := w.do(http.MethodGet, "/join/gibtsnicht", nil, c); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, erwartet 404", rec.Code)
	}
}

func TestAblehnen(t *testing.T) {
	w := neueTeamWelt(t)
	ctx := context.Background()
	trainerCookie, trainer := w.anmelden(fmt.Sprintf("t-%d@example.org", time.Now().UnixNano()))
	_ = w.team.TrainerHinzufuegen(ctx, w.verein.ID, w.bambini.ID, trainer.ID)
	_, eltern := w.anmelden(fmt.Sprintf("e-%d@example.org", time.Now().UnixNano()))
	a, err := w.team.AnfrageStellen(ctx, w.verein.ID, w.bambini.ID, eltern.ID,
		team.AnfrageDaten{Art: team.ArtKind, Vorname: "Max", Nachname: "Weg", Jahrgang: 2020})
	if err != nil {
		t.Fatal(err)
	}
	if rec := w.do(http.MethodPost, "/anfragen/"+a.ID+"/ablehnen", url.Values{}, trainerCookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("ablehnen: %d", rec.Code)
	}
	if offen, _ := w.team.OffeneAnfragen(ctx, w.verein.ID, w.bambini.ID); len(offen) != 0 {
		t.Fatal("anfrage noch offen")
	}
}

func TestLoginLeitetNachEinloesenWeiter(t *testing.T) {
	w := neueTeamWelt(t)
	rec := w.do(http.MethodGet, "/login?weiter=%2Fjoin%2Fabc", nil, nil)
	if !strings.Contains(rec.Body.String(), `value="/join/abc"`) {
		t.Fatalf("weiter fehlt im formular: %s", rec.Body.String())
	}
	w.do(http.MethodPost, "/login", url.Values{"email": {"weiter@example.org"}, "weiter": {"/join/abc"}}, nil)
	mail := w.mailer.letzte(t)
	link := regexp.MustCompile(`https://[^/]+(/auth/\S+)`).FindStringSubmatch(mail.text)
	if link == nil || !strings.Contains(link[1], "weiter=%2Fjoin%2Fabc") {
		t.Fatalf("link ohne weiter: %q", mail.text)
	}
	rec = w.do(http.MethodPost, link[1], url.Values{}, nil)
	if rec.Header().Get("Location") != "/join/abc" {
		t.Fatalf("weiterleitung = %q", rec.Header().Get("Location"))
	}
}

func TestWeiterNurAufEigeneSeiten(t *testing.T) {
	for _, in := range []string{"https://evil.example", "//evil.example", "/\\evil.example", "join", ""} {
		if got := sicheresZiel(in); got != "/" {
			t.Errorf("sicheresZiel(%q) = %q, erwartet /", in, got)
		}
	}
	if got := sicheresZiel("/join/abc"); got != "/join/abc" {
		t.Errorf("got %q", got)
	}
}
