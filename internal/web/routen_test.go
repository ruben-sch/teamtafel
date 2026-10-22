package web

import (
	"maps"
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

// geschuetzteRouten legt fest, welche Stufe jede Route verlangt. Wer eine Regel
// lockert oder verschärft, ändert sie hier bewusst mit.
var geschuetzteRouten = map[string]stufe{
	"GET /einstellungen":                                      stufeKonto,
	"POST /einstellungen/kalender":                            stufeAngemeldet,
	"POST /push/abo":                                          stufeKontoAPI,
	"DELETE /push/abo":                                        stufeKontoAPI,
	"GET /join/{token}":                                       stufeAngemeldet,
	"POST /join/{token}":                                      stufeAngemeldet,
	"GET /m/{id}":                                             stufeVerwalter,
	"POST /m/{id}/einladung":                                  stufeVerwalter,
	"GET /m/{id}/termine/neu":                                 stufeVerwalter,
	"POST /m/{id}/termine":                                    stufeVerwalter,
	"POST /m/{id}/serien":                                     stufeVerwalter,
	"POST /serien/{id}/beenden":                               stufeVerwalter,
	"GET /m/{id}/statistik":                                   stufeTrainer,
	"POST /anfragen/{id}/freigeben":                           stufeTrainer,
	"POST /anfragen/{id}/ablehnen":                            stufeTrainer,
	"GET /t/{id}":                                             stufeTeam,
	"POST /t/{id}/rueckmeldung/{spieler}":                     stufeTeam,
	"POST /t/{id}/dienste/{dienst}/eintragen":                 stufeTeam,
	"POST /t/{id}/dienste/{dienst}/austragen":                 stufeTeam,
	"POST /t/{id}":                                            stufeVerwalter,
	"POST /t/{id}/absagen":                                    stufeVerwalter,
	"POST /t/{id}/anwesenheit/{spieler}":                      stufeVerwalter,
	"POST /t/{id}/dienste":                                    stufeVerwalter,
	"POST /t/{id}/dienste/{dienst}":                           stufeVerwalter,
	"POST /t/{id}/dienste/{dienst}/loeschen":                  stufeVerwalter,
	"GET /admin":                                              stufeAdmin,
	"POST /admin/vereine":                                     stufeSuperadmin,
	"POST /admin/admins":                                      stufeVereinsadmin,
	"POST /admin/farbe":                                       stufeVereinsadmin,
	"POST /admin/mannschaften":                                stufeVereinsadmin,
	"POST /admin/mannschaften/{id}/trainer":                   stufeVereinsadmin,
	"POST /admin/mannschaften/{id}/trainer/{konto}/entfernen": stufeVereinsadmin,
}

func (w *zugriffWelt) optionen() Options {
	return Options{
		DB: fakePinger{}, Vereine: w.vereine, Auth: w.auth, Team: w.team, Termine: w.termine, Mailer: &fakeMailer{},
		Kalender: kalender.NewStore(dbtest.AppPool(w.t)), Push: &fakeAbos{}, VAPIDPublicKey: "test",
		Superadmins: []string{w.superadmin},
		BaseHost:    "teamtafel.example", Scheme: "https", Version: "test",
	}
}

// berechtigt ist ein Konto, das die Stufe gerade erfüllt.
func (w *zugriffWelt) berechtigt(s stufe) *http.Cookie {
	switch s {
	case stufeTeam:
		return w.mitglied
	case stufeVerwalter, stufeTrainer:
		return w.trainer
	case stufeAdmin, stufeVereinsadmin:
		return w.adminA
	case stufeSuperadmin:
		return w.chef
	}
	return w.fremd
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

	geschuetzt := map[string]stufe{}
	for _, ro := range rt.routen {
		if ro.grund == "" {
			geschuetzt[ro.muster] = ro.regel.stufe
		}
	}
	if !maps.Equal(geschuetzt, geschuetzteRouten) {
		t.Fatalf("Regeln der Routen = %v, want %v", geschuetzt, geschuetzteRouten)
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
			if ro.regel.stufe >= stufeVerwalter {
				if rec := do(method, a, w.mitglied); rec.Code != http.StatusForbidden {
					t.Errorf("Teammitglied: %d, want 403", rec.Code)
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
	// Erst nach allen Ablehnungen, weil hier die Handler laufen und Daten ändern.
	for _, ro := range rt.routen {
		if ro.grund != "" {
			continue
		}
		t.Run("berechtigt "+ro.muster, func(t *testing.T) {
			method, p := w.pfad(ro)
			a := "/" + w.a.Slug + p
			if ro.regel.stufe == stufeSuperadmin {
				a = p
			}
			// Wer die Regel erfüllt, kommt am Zugriffsmodul vorbei; was der Handler dann
			// antwortet (400, 404 für unbekannte Dienste …), ist hier egal.
			if rec := do(method, a, w.berechtigt(ro.regel.stufe)); rec.Code == http.StatusForbidden ||
				strings.Contains(rec.Header().Get("Location"), "/login?") {
				t.Errorf("berechtigtes Konto abgewiesen: %d %q", rec.Code, rec.Header().Get("Location"))
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
