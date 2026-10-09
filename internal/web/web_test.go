package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func TestIndexZeigtNameUndVersion(t *testing.T) {
	h := NewHandler(fakePinger{}, "1.2.3")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

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

func TestUnbekanntePfadeLiefern404(t *testing.T) {
	h := NewHandler(fakePinger{}, "dev")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gibtsnicht", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, erwartet 404", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"datenbank erreichbar", nil, http.StatusOK},
		{"datenbank weg", errors.New("connection refused"), http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler(fakePinger{err: tt.err}, "dev")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, erwartet %d", rec.Code, tt.want)
			}
		})
	}
}
