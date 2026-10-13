package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ruben-sch/teamtafel/internal/verein"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

type fakeVereine struct {
	vereine      map[string]verein.Verein
	mannschaften map[string][]verein.Mannschaft
}

func (f fakeVereine) BySlug(_ context.Context, slug string) (verein.Verein, error) {
	if v, ok := f.vereine[slug]; ok {
		return v, nil
	}
	return verein.Verein{}, verein.ErrNotFound
}

func (f fakeVereine) Mannschaften(_ context.Context, vereinID string) ([]verein.Mannschaft, error) {
	return f.mannschaften[vereinID], nil
}

func (f fakeVereine) Alle(context.Context) ([]verein.Verein, error) { return nil, nil }
func (f fakeVereine) AnlegenAusName(context.Context, string) (verein.Verein, error) {
	return verein.Verein{}, errors.ErrUnsupported
}
func (f fakeVereine) MannschaftAnlegen(context.Context, string, string, string) (verein.Mannschaft, error) {
	return verein.Mannschaft{}, errors.ErrUnsupported
}
func (f fakeVereine) AdminHinzufuegen(context.Context, string, string) error {
	return errors.ErrUnsupported
}
func (f fakeVereine) IstAdmin(context.Context, string, string) (bool, error) { return false, nil }
func (f fakeVereine) Admins(context.Context, string) ([]string, error)       { return nil, nil }
func (f fakeVereine) FarbeSetzen(context.Context, string, string) error      { return nil }

func testHandler(p Pinger) http.Handler {
	return NewHandler(Options{
		DB:       p,
		BaseHost: "teamtafel.example",
		Version:  "1.2.3",
		Vereine: fakeVereine{
			vereine: map[string]verein.Verein{"fc": {ID: "id-fc", Slug: "fc", Name: "FC Beispiel"}},
			mannschaften: map[string][]verein.Mannschaft{
				"id-fc": {{ID: "m1", Name: "Bambini", Saison: "2026/27"}},
			},
		},
	})
}

// anfrage baut einen Request auf ort, z. B. "teamtafel.example/fc" für den Verein fc.
func anfrage(method, ort, target string, body io.Reader) *http.Request {
	host, slug, _ := strings.Cut(ort, "/")
	if slug != "" {
		target = "/" + slug + target
	}
	req := httptest.NewRequest(method, target, body)
	req.Host = host
	return req
}

func get(h http.Handler, host, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestIndexAufHauptdomainZeigtNameUndVersion(t *testing.T) {
	rec := get(testHandler(fakePinger{}), "teamtafel.example", "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, erwartet 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Teamtafel", "1.2.3"} {
		if !strings.Contains(body, want) {
			t.Errorf("startseite enthält %q nicht", want)
		}
	}
}

func TestIndexImVereinspfadZeigtVereinUndMannschaften(t *testing.T) {
	rec := get(testHandler(fakePinger{}), "teamtafel.example", "/fc/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, erwartet 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"FC Beispiel", "Bambini", "2026/27", `href="/fc/login"`, `href="/fc/verein.css"`,
		`href="/fc/manifest.webmanifest"`, `href="/fc/impressum"`, `src="/static/app.js"`} {
		if !strings.Contains(body, want) {
			t.Errorf("vereinsseite enthält %q nicht", want)
		}
	}
}

func TestHauptdomainVerlinktOhneVerein(t *testing.T) {
	body := get(testHandler(fakePinger{}), "teamtafel.example", "/").Body.String()
	for _, want := range []string{`href="/login"`, `href="/verein.css"`, `href="/impressum"`} {
		if !strings.Contains(body, want) {
			t.Errorf("startseite enthält %q nicht", want)
		}
	}
}

func TestVereinspfadOhneSchraegstrichLeitetUm(t *testing.T) {
	rec := get(testHandler(fakePinger{}), "teamtafel.example", "/fc?x=1")
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/fc/?x=1" {
		t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAlteSubdomainLeitetAufPfadUm(t *testing.T) {
	h := NewHandler(Options{DB: fakePinger{}, BaseHost: "teamtafel.example", Scheme: "https", Vereine: fakeVereine{}})
	rec := get(h, "FC.teamtafel.example", "/t/5?a=b")
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "https://teamtafel.example/fc/t/5?a=b" {
		t.Fatalf("GET: status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/abc", nil)
	req.Host = "fc.teamtafel.example:8443"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "https://teamtafel.example:8443/fc/auth/abc" {
		t.Fatalf("POST: status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestUnbekannterVereinUndFremderHostLiefern404(t *testing.T) {
	h := testHandler(fakePinger{})
	for _, ziel := range []struct{ host, pfad string }{
		{"teamtafel.example", "/gibtsnicht/"},
		{"teamtafel.example", "/gibtsnicht/m/1"},
		{"a.b.teamtafel.example", "/"},
		{"fremd.example", "/"},
	} {
		if rec := get(h, ziel.host, ziel.pfad); rec.Code != http.StatusNotFound {
			t.Errorf("%s%s: status = %d, erwartet 404", ziel.host, ziel.pfad, rec.Code)
		}
	}
}

func TestManifestStartetImVerein(t *testing.T) {
	h := testHandler(fakePinger{})
	if body := get(h, "teamtafel.example", "/fc/manifest.webmanifest").Body.String(); !strings.Contains(body, `"start_url": "/fc/"`) {
		t.Errorf("vereinsmanifest: %s", body)
	}
	if body := get(h, "teamtafel.example", "/manifest.webmanifest").Body.String(); !strings.Contains(body, `"start_url": "/"`) {
		t.Errorf("manifest: %s", body)
	}
}

func TestUnbekanntePfadeLiefern404(t *testing.T) {
	if rec := get(testHandler(fakePinger{}), "teamtafel.example", "/gibtsnicht"); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, erwartet 404", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	tests := []struct {
		name string
		host string
		err  error
		want int
	}{
		{"datenbank erreichbar", "teamtafel.example", nil, http.StatusOK},
		{"datenbank weg", "teamtafel.example", errors.New("connection refused"), http.StatusServiceUnavailable},
		// Der Docker-Healthcheck ruft localhost auf, nicht die Domain.
		{"über localhost", "localhost:8080", nil, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := get(testHandler(fakePinger{err: tt.err}), tt.host, "/healthz"); rec.Code != tt.want {
				t.Fatalf("status = %d, erwartet %d", rec.Code, tt.want)
			}
		})
	}
}
