package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/dbtest"
)

type gesendeteMail struct{ an, betreff, text string }

type fakeMailer struct {
	mu    sync.Mutex
	mails []gesendeteMail
}

func (f *fakeMailer) Senden(_ context.Context, an, betreff, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mails = append(f.mails, gesendeteMail{an, betreff, text})
	return nil
}

func (f *fakeMailer) letzte(t *testing.T) gesendeteMail {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.mails) == 0 {
		t.Fatal("keine mail versendet")
	}
	return f.mails[len(f.mails)-1]
}

func loginHandler(t *testing.T, m *fakeMailer) http.Handler {
	t.Helper()
	return NewHandler(Options{
		DB:       fakePinger{},
		Vereine:  fakeVereine{},
		Auth:     auth.NewStore(dbtest.AppPool(t)),
		Mailer:   m,
		BaseHost: "teamtafel.example",
		Scheme:   "https",
		Version:  "test",
	})
}

func do(h http.Handler, method, target string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, body)
	req.Host = "teamtafel.example"
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var linkPattern = regexp.MustCompile(`https://teamtafel\.example(/auth/[A-Za-z0-9_-]+)`)

func TestMagicLinkLoginEndeZuEnde(t *testing.T) {
	m := &fakeMailer{}
	h := loginHandler(t, m)

	if rec := do(h, http.MethodGet, "/login", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="email"`) {
		t.Fatalf("GET /login: %d", rec.Code)
	}

	rec := do(h, http.MethodPost, "/login", url.Values{"email": {"Eltern@Example.org"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Postfach") {
		t.Fatalf("POST /login: %d %s", rec.Code, rec.Body.String())
	}
	mail := m.letzte(t)
	if mail.an != "eltern@example.org" {
		t.Fatalf("mail an %q", mail.an)
	}
	match := linkPattern.FindStringSubmatch(mail.text)
	if match == nil {
		t.Fatalf("kein link in der mail: %q", mail.text)
	}
	authPath := match[1]

	// GET verbraucht den Link nicht (Mail-Scanner rufen Links vorab auf).
	for i := 0; i < 2; i++ {
		if rec := do(h, http.MethodGet, authPath, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<form") {
			t.Fatalf("GET %s: %d", authPath, rec.Code)
		}
	}

	rec = do(h, http.MethodPost, authPath, url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST %s: %d %s", authPath, rec.Code, rec.Body.String())
	}
	var sess *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "__Host-session" {
			sess = c
		}
	}
	if sess == nil || !sess.HttpOnly || !sess.Secure || sess.SameSite != http.SameSiteLaxMode || sess.Path != "/" {
		t.Fatalf("session-cookie fehlt oder unsicher: %+v", sess)
	}

	rec = do(h, http.MethodGet, "/", nil, sess)
	if !strings.Contains(rec.Body.String(), "eltern@example.org") {
		t.Fatalf("startseite zeigt angemeldete person nicht: %s", rec.Body.String())
	}

	// Link ist verbraucht.
	if rec := do(h, http.MethodPost, authPath, url.Values{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("zweites einlösen: %d", rec.Code)
	}

	rec = do(h, http.MethodPost, "/logout", url.Values{}, sess)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("logout: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/", nil, sess); strings.Contains(rec.Body.String(), "eltern@example.org") {
		t.Fatal("nach logout noch angemeldet")
	}
}

func TestLoginMitUngueltigerAdresse(t *testing.T) {
	m := &fakeMailer{}
	rec := do(loginHandler(t, m), http.MethodPost, "/login", url.Values{"email": {"keine-adresse"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, erwartet 400", rec.Code)
	}
	if len(m.mails) != 0 {
		t.Fatal("bei ungültiger adresse darf keine mail rausgehen")
	}
}

func TestLoginRateLimitProAdresse(t *testing.T) {
	m := &fakeMailer{}
	h := loginHandler(t, m)
	var last int
	for i := 0; i < 6; i++ {
		last = do(h, http.MethodPost, "/login", url.Values{"email": {"limit@example.org"}}).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("sechster versuch: status = %d, erwartet 429", last)
	}
	if len(m.mails) != 5 {
		t.Fatalf("%d mails, erwartet 5", len(m.mails))
	}
}

func TestCrossOriginPostWirdAbgelehnt(t *testing.T) {
	h := loginHandler(t, &fakeMailer{})
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=a%40b.de"))
	req.Host = "teamtafel.example"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, erwartet 403", rec.Code)
	}
}
