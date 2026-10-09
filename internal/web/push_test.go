package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ruben-sch/teamtafel/internal/push"
)

type fakeAbos struct {
	gespeichert map[string]push.Abo // konto -> abo
	geloescht   []string
}

func (f *fakeAbos) Speichern(_ context.Context, konto string, a push.Abo) error {
	if !strings.HasPrefix(a.Endpoint, "https://") {
		return push.ErrUngueltig
	}
	f.gespeichert[konto] = a
	return nil
}

func (f *fakeAbos) Loeschen(_ context.Context, konto, endpoint string) error {
	f.geloescht = append(f.geloescht, konto+" "+endpoint)
	return nil
}

func TestStatischeDateien(t *testing.T) {
	w := neueAdminWelt(t)
	for pfad, typ := range map[string]string{
		"/sw.js": "text/javascript", "/manifest.webmanifest": "application/manifest+json",
		"/static/app.js": "text/javascript", "/static/icon-192.png": "image/png",
	} {
		rec := w.do("teamtafel.example", http.MethodGet, pfad, nil, nil)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), typ) {
			t.Errorf("%s: %d %q", pfad, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

func TestPushAboSpeichernUndLoeschen(t *testing.T) {
	w := neueAdminWelt(t)
	abos := &fakeAbos{gespeichert: map[string]push.Abo{}}
	w.h = NewHandler(Options{DB: fakePinger{}, Vereine: w.verein, Auth: w.auth, Team: w.team, Mailer: &fakeMailer{},
		Push: abos, VAPIDPublicKey: "BKEY", BaseHost: "teamtafel.example", Scheme: "https"})
	const host = "teamtafel.example"
	email := fmt.Sprintf("push-%d@example.org", time.Now().UnixNano())
	c := w.anmelden(host, email)
	k, _ := w.auth.KontoFuer(context.Background(), email)

	json := func(method, body string, c *http.Cookie) int {
		req := httptest.NewRequest(method, "/push/abo", strings.NewReader(body))
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		if c != nil {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		w.h.ServeHTTP(rec, req)
		return rec.Code
	}
	abo := `{"endpoint":"https://push.example/1","expirationTime":null,"keys":{"p256dh":"BP","auth":"AU"}}`
	if code := json(http.MethodPost, abo, nil); code != http.StatusUnauthorized {
		t.Errorf("ohne login: %d", code)
	}
	if code := json(http.MethodPost, abo, c); code != http.StatusNoContent {
		t.Fatalf("speichern: %d", code)
	}
	if a := abos.gespeichert[k.ID]; a.Endpoint != "https://push.example/1" || a.Keys.P256dh != "BP" || a.Keys.Auth != "AU" {
		t.Errorf("gespeichert: %+v", a)
	}
	if code := json(http.MethodPost, `{"endpoint":"http://x"}`, c); code != http.StatusBadRequest {
		t.Errorf("ungültig: %d", code)
	}
	if code := json(http.MethodPost, `kaputt`, c); code != http.StatusBadRequest {
		t.Errorf("kein json: %d", code)
	}
	if code := json(http.MethodDelete, `{"endpoint":"https://push.example/1"}`, c); code != http.StatusNoContent ||
		len(abos.geloescht) != 1 || abos.geloescht[0] != k.ID+" https://push.example/1" {
		t.Errorf("löschen: %d %v", code, abos.geloescht)
	}

	body := w.do(host, http.MethodGet, "/einstellungen", nil, c).Body.String()
	if !strings.Contains(body, `data-key="BKEY"`) {
		t.Errorf("einstellungen ohne push-schalter")
	}
	if rec := w.do(host, http.MethodGet, "/einstellungen", nil, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("einstellungen ohne login: %d", rec.Code)
	}
}

func TestOhneVAPIDNurEmail(t *testing.T) {
	w := neueAdminWelt(t)
	const host = "teamtafel.example"
	c := w.anmelden(host, fmt.Sprintf("np-%d@example.org", time.Now().UnixNano()))
	body := w.do(host, http.MethodGet, "/einstellungen", nil, c).Body.String()
	if strings.Contains(body, `id="push"`) || !strings.Contains(body, "per E-Mail") {
		t.Errorf("einstellungen ohne vapid: %s", body)
	}
	if rec := w.do(host, http.MethodPost, "/push/abo", nil, c); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("push/abo ohne vapid: %d", rec.Code)
	}
}
