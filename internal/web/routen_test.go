package web

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ruben-sch/teamtafel/internal/dbtest"
	"github.com/ruben-sch/teamtafel/internal/kalender"
)

// oeffentlicheRouten sind alle Routen ohne Login. Eine neue öffentliche Route muss
// hier bewusst eingetragen werden.
var oeffentlicheRouten = []string{
	"GET /{$}",
	"GET /login",
	"POST /login",
	"GET /auth/{token}",
	"POST /auth/{token}",
	"POST /logout",
	"GET /sw.js",
	"GET /manifest.webmanifest",
	"GET /static/",
	"GET /verein.css",
	"GET /impressum",
	"GET /datenschutz",
	"GET /kalender/{datei}",
}

func (w *zugriffWelt) optionen() Options {
	return Options{
		DB: fakePinger{}, Vereine: w.vereine, Auth: w.auth, Team: w.team, Termine: w.termine, Mailer: &fakeMailer{},
		Kalender: kalender.NewStore(dbtest.AppPool(w.t)), Push: &fakeAbos{}, VAPIDPublicKey: "test",
		Superadmins: []string{w.superadmin},
		BaseHost:    "teamtafel.example", Scheme: "https", Version: "test",
	}
}

var platzhalter = regexp.MustCompile(`\{[a-z]+\}`)

// pfad setzt für {id} das Objekt aus Verein A ein, für andere Platzhalter eine unbekannte UUID.
func (w *zugriffWelt) pfad(ro route) (string, string) {
	method, muster, _ := strings.Cut(ro.muster, " ")
	ids := map[string]string{"mannschaft": w.mannschaft, "termin": w.termin, "serie": w.serie, "anfrage": w.anfrage}
	p := strings.ReplaceAll(muster, "{id}", ids[ro.art])
	p = platzhalter.ReplaceAllString(p, "00000000-0000-4000-8000-000000000000")
	return method, p
}

func TestJedeRouteHatEineRegel(t *testing.T) {
	w := neueZugriffWelt(t)
	o := w.optionen()
	rt := routenVon(o)
	h := NewHandler(o)

	var offen []string
	for _, ro := range rt.routen {
		if ro.grund != "" {
			offen = append(offen, ro.muster)
		}
	}
	slices.Sort(offen)
	erwartet := slices.Sorted(slices.Values(oeffentlicheRouten))
	if !slices.Equal(offen, erwartet) {
		t.Fatalf("öffentliche Routen = %v, want %v", offen, erwartet)
	}

	do := func(method, pfad string, c *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, pfad, nil)
		req.Host = "teamtafel.example"
		if method != http.MethodGet {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if c != nil {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, ro := range rt.routen {
		if ro.grund != "" {
			continue
		}
		t.Run(ro.muster, func(t *testing.T) {
			method, p := w.pfad(ro)
			a, b := "/"+w.a.Slug+p, "/"+w.b.Slug+p
			if ro.regel.stufe == stufeSuperadmin {
				a = p
			}
			rec := do(method, a, nil)
			switch {
			case ro.regel.stufe == stufeKontoAPI:
				if rec.Code != http.StatusUnauthorized {
					t.Errorf("anonym: %d, want 401", rec.Code)
				}
			case rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "/login?weiter="):
				t.Errorf("anonym: %d %q, want Weiterleitung zum Login", rec.Code, rec.Header().Get("Location"))
			}
			if ro.regel.stufe > stufeAngemeldet {
				if rec := do(method, a, w.fremd); rec.Code != http.StatusForbidden {
					t.Errorf("fremdes Konto: %d, want 403", rec.Code)
				}
			}
			if ro.art != "" {
				for name, c := range map[string]*http.Cookie{"admin von b": w.adminB, "superadmin": w.chef} {
					if rec := do(method, b, c); rec.Code != http.StatusNotFound {
						t.Errorf("%s mit ID aus a unter b: %d, want 404", name, rec.Code)
					}
				}
			}
		})
	}
}

// Statistik und Beitrittsanfragen sind für die Arbeit mit der Mannschaft gedacht;
// Vereinsadmins und Plattform-Admins sehen sie nicht.
func TestAdminsNichtAufTrainerRouten(t *testing.T) {
	w := neueZugriffWelt(t)
	h := NewHandler(w.optionen())
	a := "/" + w.a.Slug
	for _, ziel := range []struct{ method, pfad string }{
		{http.MethodGet, a + "/m/" + w.mannschaft + "/statistik"},
		{http.MethodPost, a + "/anfragen/" + w.anfrage + "/freigeben"},
		{http.MethodPost, a + "/anfragen/" + w.anfrage + "/ablehnen"},
	} {
		for name, c := range map[string]*http.Cookie{"vereinsadmin": w.adminA, "superadmin": w.chef} {
			req := httptest.NewRequest(ziel.method, ziel.pfad, nil)
			req.Host = "teamtafel.example"
			req.AddCookie(c)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s als %s: %d, want 403", ziel.method, ziel.pfad, name, rec.Code)
			}
		}
	}
}
