package web

import (
	"context"
	"errors"
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
func (f fakeVereine) Anlegen(context.Context, string, string) (verein.Verein, error) {
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

func TestIndexAufVereinsSubdomainZeigtVereinUndMannschaften(t *testing.T) {
	rec := get(testHandler(fakePinger{}), "fc.teamtafel.example:443", "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, erwartet 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"FC Beispiel", "Bambini", "2026/27"} {
		if !strings.Contains(body, want) {
			t.Errorf("vereinsseite enthält %q nicht", want)
		}
	}
}

func TestUnbekannterVereinUndFremderHostLiefern404(t *testing.T) {
	h := testHandler(fakePinger{})
	for _, host := range []string{"gibtsnicht.teamtafel.example", "a.b.teamtafel.example", "fremd.example"} {
		if rec := get(h, host, "/"); rec.Code != http.StatusNotFound {
			t.Errorf("host %s: status = %d, erwartet 404", host, rec.Code)
		}
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
