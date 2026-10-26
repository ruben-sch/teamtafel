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
	"github.com/ruben-sch/teamtafel/internal/termin"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

// zugriffWelt: Verein A mit Mannschaft, Termin, Serie und offener Anfrage, Verein B daneben.
type zugriffWelt struct {
	t       *testing.T
	auth    *auth.Store
	vereine *verein.Store
	team    *team.Store
	termine *termin.Store
	a, b    verein.Verein
	// Objekte in Verein A.
	mannschaft, termin, serie, anfrage string
	// Sitzungen; anonym ist nil.
	trainer, mitglied, fremd, adminA, adminB, chef *http.Cookie
	superadmin                                     string
	// antragsteller stellt die offenen Beitrittsanfragen.
	antragsteller string
}

func neueZugriffWelt(t *testing.T) *zugriffWelt {
	t.Helper()
	pool := dbtest.AppPool(t)
	ctx := context.Background()
	n := time.Now().UnixNano()
	w := &zugriffWelt{t: t, auth: auth.NewStore(pool), vereine: verein.NewStore(pool), team: team.NewStore(pool),
		termine: termin.NewStore(pool), superadmin: fmt.Sprintf("chef-%d@example.org", n)}
	var err error
	if w.a, err = w.vereine.Anlegen(ctx, fmt.Sprintf("za-%d", n), "FC A"); err != nil {
		t.Fatal(err)
	}
	if w.b, err = w.vereine.Anlegen(ctx, fmt.Sprintf("zb-%d", n), "FC B"); err != nil {
		t.Fatal(err)
	}
	m, err := w.vereine.MannschaftAnlegen(ctx, w.a.ID, "2026/27", "Bambini")
	if err != nil {
		t.Fatal(err)
	}
	w.mannschaft = m.ID
	var trainer, mitglied, adminA, adminB string
	w.trainer, trainer = w.anmelden(fmt.Sprintf("zt-%d@example.org", n))
	w.mitglied, mitglied = w.anmelden(fmt.Sprintf("zm-%d@example.org", n))
	w.fremd, _ = w.anmelden(fmt.Sprintf("zf-%d@example.org", n))
	w.adminA, adminA = w.anmelden(fmt.Sprintf("zaa-%d@example.org", n))
	w.adminB, adminB = w.anmelden(fmt.Sprintf("zab-%d@example.org", n))
	w.chef, _ = w.anmelden(w.superadmin)
	if err := w.team.TrainerHinzufuegen(ctx, w.a.ID, m.ID, trainer); err != nil {
		t.Fatal(err)
	}
	if err := w.vereine.AdminHinzufuegen(ctx, w.a.ID, adminA); err != nil {
		t.Fatal(err)
	}
	if err := w.vereine.AdminHinzufuegen(ctx, w.b.ID, adminB); err != nil {
		t.Fatal(err)
	}
	an, err := w.team.AnfrageStellen(ctx, w.a.ID, m.ID, mitglied, team.AnfrageDaten{Art: team.ArtKind, Vorname: "Ben", Nachname: "Ball", Jahrgang: 2020})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.team.Freigeben(ctx, w.a.ID, an.ID, trainer, false); err != nil {
		t.Fatal(err)
	}
	offen, err := w.team.AnfrageStellen(ctx, w.a.ID, m.ID, adminB, team.AnfrageDaten{Art: team.ArtKind, Vorname: "Lia", Nachname: "Ball", Jahrgang: 2020})
	if err != nil {
		t.Fatal(err)
	}
	w.anfrage, w.antragsteller = offen.ID, adminB
	b := time.Now().In(termin.Zeitzone).AddDate(0, 0, 3).Truncate(time.Hour)
	tm, err := w.termine.Anlegen(ctx, w.a.ID, m.ID, "", termin.Daten{Typ: termin.TypTraining, Beginn: b, Ende: b.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	w.termin = tm.ID
	sr, err := w.termine.SerieAnlegen(ctx, w.a.ID, m.ID, termin.SerieDaten{Wochentag: time.Monday, Uhrzeit: "17:00",
		Dauer: time.Hour, GueltigVon: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	w.serie = sr.ID
	return w
}

func (w *zugriffWelt) anmelden(email string) (*http.Cookie, string) {
	w.t.Helper()
	tok, err := w.auth.LinkAnfordern(context.Background(), email)
	if err != nil {
		w.t.Fatal(err)
	}
	sess, k, err := w.auth.Einloesen(context.Background(), tok)
	if err != nil {
		w.t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: sess}, k.ID
}

func (w *zugriffWelt) zugriff() *zugriff {
	return &zugriff{
		scheme:  "https",
		rollen:  &rollen{vereine: w.vereine, superadmins: map[string]bool{w.superadmin: true}},
		team:    w.team,
		termine: w.termine,
	}
}

// aufgerufen hält fest, was der Handler bekommen hat.
type aufgerufen struct {
	ok           bool
	verein       string
	imVerein     bool
	konto        string
	objekt       string
	mannschaftID string
	trainer      bool
	verwalter    bool
}

// merke ist ein Handler, der nur festhält, welchen Zugang er bekommen hat.
func merke[O any](got *aufgerufen, id func(O) string) Handler[O] {
	return func(_ http.ResponseWriter, _ *http.Request, zg Zugang[O]) {
		*got = aufgerufen{ok: true, verein: zg.Verein.ID, imVerein: zg.ImVerein, konto: zg.Konto.ID, objekt: id(zg.Objekt),
			mannschaftID: zg.MannschaftID, trainer: zg.Trainer, verwalter: zg.Verwalter}
	}
}

// stubRouter registriert je Regel und Objekt eine Route, deren Handler nur mitschreibt.
func (w *zugriffWelt) stubRouter(got *aufgerufen) http.Handler {
	z := w.zugriff()
	rt := neuerRouter(z)
	terminID := func(t termin.Termin) string { return t.ID }
	mannschaftID := func(m verein.Mannschaft) string { return m.ID }
	mitObjekt(rt, "GET /team/t/{id}", TeamMitglied, z.termin(), merke(got, terminID))
	mitObjekt(rt, "POST /verwalter/t/{id}", Verwalter, z.termin(), merke(got, terminID))
	mitObjekt(rt, "GET /trainer/m/{id}", Trainer, z.mannschaft(), merke(got, mannschaftID))
	mitObjekt(rt, "GET /verwalter/s/{id}", Verwalter, z.serie(), merke(got, func(s termin.Serie) string { return s.ID }))
	mitObjekt(rt, "GET /trainer/a/{id}", Trainer, z.anfrage(), merke(got, func(a beitrittsanfrage) string { return a.ID }))
	mitObjekt(rt, "GET /admin/m/{id}", Vereinsadmin, z.mannschaft(), merke(got, mannschaftID))
	keins := func(Keins) string { return "" }
	rt.ohneObjekt("GET /konto", Konto, merke(got, keins))
	rt.ohneObjekt("POST /api", KontoAPI, merke(got, keins))
	rt.ohneObjekt("POST /angemeldet", Angemeldet, merke(got, keins))
	rt.ohneObjekt("GET /admin", Admin, merke(got, keins))
	rt.ohneObjekt("GET /vereinsadmin", Vereinsadmin, merke(got, keins))
	rt.ohneObjekt("POST /vereinsadmin", Vereinsadmin, merke(got, keins))
	rt.ohneObjekt("GET /superadmin", Superadmin, merke(got, keins))
	rt.ohneObjekt("POST /admin", Admin, merke(got, keins))
	rt.ohneObjekt("POST /superadmin", Superadmin, merke(got, keins))
	rt.oeffentlich("GET /offen", "Test", func(rw http.ResponseWriter, r *http.Request) { *got = aufgerufen{ok: true} })
	return mandant("https", "teamtafel.example", w.vereine, sitzung(w.auth, rt))
}

func TestZugriffsmodul(t *testing.T) {
	w := neueZugriffWelt(t)
	unbekannt := "00000000-0000-4000-8000-000000000000"
	a, b := "/"+w.a.Slug, "/"+w.b.Slug
	login := func(weiter string) string { return a + "/login?weiter=" + url.QueryEscape(weiter) }
	tests := []struct {
		name    string
		method  string
		pfad    string
		konto   *http.Cookie
		referer string
		status  int
		ort     string // Location bei Weiterleitungen
		text    string // Teil der 403-Seite
		want    aufgerufen
	}{
		// TeamMitglied am Termin.
		{name: "anonym zum login", pfad: a + "/team/t/" + w.termin, status: http.StatusSeeOther, ort: login("/team/t/" + w.termin)},
		{name: "fremder 403", pfad: a + "/team/t/" + w.termin, konto: w.fremd, status: http.StatusForbidden, text: `href="` + a + `/"`},
		{name: "mitglied darf", pfad: a + "/team/t/" + w.termin, konto: w.mitglied, status: http.StatusOK,
			want: aufgerufen{ok: true, objekt: w.termin, mannschaftID: w.mannschaft}},
		{name: "trainer ist verwalter", pfad: a + "/team/t/" + w.termin, konto: w.trainer, status: http.StatusOK,
			want: aufgerufen{ok: true, objekt: w.termin, mannschaftID: w.mannschaft, trainer: true, verwalter: true}},
		{name: "vereinsadmin ist verwalter", pfad: a + "/team/t/" + w.termin, konto: w.adminA, status: http.StatusOK,
			want: aufgerufen{ok: true, objekt: w.termin, mannschaftID: w.mannschaft, verwalter: true}},
		{name: "superadmin ist verwalter", pfad: a + "/team/t/" + w.termin, konto: w.chef, status: http.StatusOK,
			want: aufgerufen{ok: true, objekt: w.termin, mannschaftID: w.mannschaft, verwalter: true}},
		{name: "unbekannter termin 404", pfad: a + "/team/t/" + unbekannt, konto: w.adminA, status: http.StatusNotFound},
		{name: "keine uuid 404", pfad: a + "/team/t/abc", konto: w.adminA, status: http.StatusNotFound},
		{name: "termin aus a unter b 404", pfad: b + "/team/t/" + w.termin, konto: w.adminB, status: http.StatusNotFound},
		{name: "termin aus a unter b auch für superadmin 404", pfad: b + "/team/t/" + w.termin, konto: w.chef, status: http.StatusNotFound},
		{name: "ohne verein 404", pfad: "/team/t/" + w.termin, konto: w.chef, status: http.StatusNotFound},

		// Verwalter am Termin; Teammitglieder kommen zurück zum Termin.
		{name: "mitglied ist kein verwalter", method: http.MethodPost, pfad: a + "/verwalter/t/" + w.termin, konto: w.mitglied,
			status: http.StatusForbidden, text: `href="` + a + `/t/` + w.termin + `"`},
		{name: "fremder am termin erfährt, dass ihn nur das team sieht", method: http.MethodPost, pfad: a + "/verwalter/t/" + w.termin,
			konto: w.fremd, status: http.StatusForbidden, text: "Diesen Termin sieht nur das Team."},
		{name: "mitglied erfährt den text der regel", method: http.MethodPost, pfad: a + "/verwalter/t/" + w.termin,
			konto: w.mitglied, status: http.StatusForbidden, text: "Diese Seite sehen nur die Trainer der Mannschaft."},
		{name: "trainer verwaltet", method: http.MethodPost, pfad: a + "/verwalter/t/" + w.termin, konto: w.trainer, status: http.StatusOK,
			want: aufgerufen{ok: true, objekt: w.termin, mannschaftID: w.mannschaft, trainer: true, verwalter: true}},
		{name: "post anonym zurück zur herkunft", method: http.MethodPost, pfad: a + "/verwalter/t/" + w.termin,
			referer: "https://teamtafel.example" + a + "/t/" + w.termin, status: http.StatusSeeOther, ort: login("/t/" + w.termin)},
		{name: "post anonym herkunft mit großem host", method: http.MethodPost, pfad: a + "/verwalter/t/" + w.termin,
			referer: "https://TeamTafel.example" + a + "/t/" + w.termin, status: http.StatusSeeOther, ort: login("/t/" + w.termin)},
		{name: "post anonym herkunft mit anderem schema", method: http.MethodPost, pfad: a + "/verwalter/t/" + w.termin,
			referer: "http://teamtafel.example" + a + "/t/" + w.termin, status: http.StatusSeeOther, ort: login("/")},
		{name: "post anonym auf admin-route ohne herkunft zur verwaltung", method: http.MethodPost, pfad: a + "/vereinsadmin",
			status: http.StatusSeeOther, ort: login("/admin")},
		{name: "post anonym auf admin-route mit herkunft", method: http.MethodPost, pfad: a + "/vereinsadmin",
			referer: "https://teamtafel.example" + a + "/admin", status: http.StatusSeeOther, ort: login("/admin")},
		{name: "post anonym auf adminseite ohne herkunft zur verwaltung", method: http.MethodPost, pfad: a + "/admin",
			status: http.StatusSeeOther, ort: login("/admin")},
		{name: "post anonym auf superadmin ohne herkunft zur verwaltung", method: http.MethodPost, pfad: "/superadmin",
			status: http.StatusSeeOther, ort: "/login?weiter=" + url.QueryEscape("/admin")},
		{name: "post anonym fremde herkunft", method: http.MethodPost, pfad: a + "/verwalter/t/" + w.termin,
			referer: "https://boese.example" + a + "/t/" + w.termin, status: http.StatusSeeOther, ort: login("/")},
		{name: "post anonym ohne herkunft", method: http.MethodPost, pfad: a + "/verwalter/t/" + w.termin,
			status: http.StatusSeeOther, ort: login("/")},

		// Serie und Anfrage laden ihre Mannschaft.
		{name: "serie für trainer", pfad: a + "/verwalter/s/" + w.serie, konto: w.trainer, status: http.StatusOK,
			want: aufgerufen{ok: true, objekt: w.serie, mannschaftID: w.mannschaft, trainer: true, verwalter: true}},
		{name: "serie für mitglied 403", pfad: a + "/verwalter/s/" + w.serie, konto: w.mitglied, status: http.StatusForbidden},
		{name: "unbekannte serie 404", pfad: a + "/verwalter/s/" + unbekannt, konto: w.trainer, status: http.StatusNotFound},
		{name: "anfrage für trainer", pfad: a + "/trainer/a/" + w.anfrage, konto: w.trainer, status: http.StatusOK,
			want: aufgerufen{ok: true, objekt: w.anfrage, mannschaftID: w.mannschaft, trainer: true, verwalter: true}},
		{name: "anfrage nicht für admins", pfad: a + "/trainer/a/" + w.anfrage, konto: w.adminA, status: http.StatusForbidden},
		{name: "unbekannte anfrage 404", pfad: a + "/trainer/a/" + unbekannt, konto: w.trainer, status: http.StatusNotFound},

		// Trainer an der Mannschaft schließt Admins aus.
		{name: "trainer-route für trainer", pfad: a + "/trainer/m/" + w.mannschaft, konto: w.trainer, status: http.StatusOK,
			want: aufgerufen{ok: true, objekt: w.mannschaft, mannschaftID: w.mannschaft, trainer: true, verwalter: true}},
		{name: "trainer-route nicht für vereinsadmin", pfad: a + "/trainer/m/" + w.mannschaft, konto: w.adminA, status: http.StatusForbidden},
		{name: "trainer-route nicht für superadmin", pfad: a + "/trainer/m/" + w.mannschaft, konto: w.chef, status: http.StatusForbidden},

		// Vereinsadmin an der Mannschaft: fremde Mannschaft ist 404, nicht 500 (#12).
		{name: "admin-mannschaft für vereinsadmin", pfad: a + "/admin/m/" + w.mannschaft, konto: w.adminA, status: http.StatusOK,
			want: aufgerufen{ok: true, objekt: w.mannschaft, mannschaftID: w.mannschaft, verwalter: true}},
		{name: "admin-mannschaft nicht für trainer", pfad: a + "/admin/m/" + w.mannschaft, konto: w.trainer, status: http.StatusForbidden},
		{name: "unbekannte admin-mannschaft für trainer 403", pfad: a + "/admin/m/" + unbekannt, konto: w.trainer, status: http.StatusForbidden},
		{name: "admin-mannschaft ohne uuid für trainer 403", pfad: a + "/admin/m/abc", konto: w.trainer, status: http.StatusForbidden},
		{name: "unbekannte admin-mannschaft für vereinsadmin 404", pfad: a + "/admin/m/" + unbekannt, konto: w.adminA, status: http.StatusNotFound},
		{name: "admin-mannschaft ohne verein anonym zum login", pfad: "/admin/m/" + w.mannschaft, status: http.StatusSeeOther,
			ort: "/login?weiter=" + url.QueryEscape("/admin/m/"+w.mannschaft)},
		{name: "admin-mannschaft ohne verein für fremden 403", pfad: "/admin/m/" + w.mannschaft, konto: w.fremd, status: http.StatusForbidden},
		{name: "admin-mannschaft ohne verein für superadmin 404", pfad: "/admin/m/" + w.mannschaft, konto: w.chef, status: http.StatusNotFound},
		{name: "mannschaft aus a unter b 404", pfad: b + "/admin/m/" + w.mannschaft, konto: w.adminB, status: http.StatusNotFound},

		// Routen ohne Objekt.
		{name: "konto ohne verein", pfad: "/konto", konto: w.fremd, status: http.StatusOK, want: aufgerufen{ok: true}},
		{name: "konto im verein", pfad: a + "/konto", konto: w.fremd, status: http.StatusOK, want: aufgerufen{ok: true, imVerein: true}},
		{name: "konto anonym", pfad: "/konto", status: http.StatusSeeOther, ort: "/login?weiter=" + url.QueryEscape("/konto")},
		{name: "api anonym 401", method: http.MethodPost, pfad: "/api", status: http.StatusUnauthorized},
		{name: "api angemeldet", method: http.MethodPost, pfad: "/api", konto: w.fremd, status: http.StatusOK, want: aufgerufen{ok: true}},
		{name: "angemeldet braucht verein", method: http.MethodPost, pfad: "/angemeldet", konto: w.fremd, status: http.StatusNotFound},
		{name: "angemeldet im verein", method: http.MethodPost, pfad: a + "/angemeldet", konto: w.fremd, status: http.StatusOK,
			want: aufgerufen{ok: true, imVerein: true}},
		{name: "admin für vereinsadmin", pfad: a + "/admin", konto: w.adminA, status: http.StatusOK,
			want: aufgerufen{ok: true, imVerein: true, verwalter: true}},
		{name: "admin nicht im fremden verein", pfad: b + "/admin", konto: w.adminA, status: http.StatusForbidden},
		{name: "admin auf hauptdomain nur superadmin", pfad: "/admin", konto: w.adminA, status: http.StatusForbidden},
		{name: "admin auf hauptdomain für superadmin", pfad: "/admin", konto: w.chef, status: http.StatusOK, want: aufgerufen{ok: true, verwalter: true}},
		{name: "vereinsadmin braucht verein", pfad: "/vereinsadmin", konto: w.chef, status: http.StatusNotFound},
		{name: "vereinsadmin ohne verein anonym zum login", pfad: "/vereinsadmin", status: http.StatusSeeOther,
			ort: "/login?weiter=" + url.QueryEscape("/vereinsadmin")},
		{name: "vereinsadmin ohne verein für fremden 403", pfad: "/vereinsadmin", konto: w.fremd, status: http.StatusForbidden},
		{name: "vereinsadmin für fremden 403", pfad: a + "/vereinsadmin", konto: w.fremd, status: http.StatusForbidden},
		{name: "superadmin nur auf hauptdomain", pfad: a + "/superadmin", konto: w.chef, status: http.StatusNotFound},
		{name: "superadmin-route im verein für fremden 403", pfad: a + "/superadmin", konto: w.fremd, status: http.StatusForbidden},
		{name: "superadmin-route im verein für vereinsadmin 404", pfad: a + "/superadmin", konto: w.adminA, status: http.StatusNotFound},
		{name: "superadmin nicht für vereinsadmin", pfad: "/superadmin", konto: w.adminA, status: http.StatusForbidden},
		{name: "superadmin für superadmin", pfad: "/superadmin", konto: w.chef, status: http.StatusOK, want: aufgerufen{ok: true, verwalter: true}},
		{name: "öffentlich ohne login", pfad: "/offen", status: http.StatusOK, want: aufgerufen{ok: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got aufgerufen
			h := w.stubRouter(&got)
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			req := httptest.NewRequest(method, tt.pfad, nil)
			req.Host = "teamtafel.example"
			if tt.referer != "" {
				req.Header.Set("Referer", tt.referer)
			}
			if tt.konto != nil {
				req.AddCookie(tt.konto)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.status, rec.Body.String())
			}
			if tt.ort != "" && rec.Header().Get("Location") != tt.ort {
				t.Errorf("Location = %q, want %q", rec.Header().Get("Location"), tt.ort)
			}
			if tt.text != "" && !strings.Contains(rec.Body.String(), tt.text) {
				t.Errorf("antwort ohne %q: %s", tt.text, rec.Body.String())
			}
			if tt.status != http.StatusOK {
				if got.ok {
					t.Errorf("handler lief trotz %d", tt.status)
				}
				return
			}
			// Verein und Konto prüft TestZugangTraegtVereinUndKonto; Objekt-Routen laufen immer im Verein.
			got.verein, got.konto = "", ""
			if got.objekt != "" {
				got.imVerein = false
			}
			if got != tt.want {
				t.Errorf("zugang = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestZugangTraegtVereinUndKonto(t *testing.T) {
	w := neueZugriffWelt(t)
	var got aufgerufen
	req := httptest.NewRequest(http.MethodGet, "/"+w.a.Slug+"/team/t/"+w.termin, nil)
	req.Host = "teamtafel.example"
	req.AddCookie(w.mitglied)
	w.stubRouter(&got).ServeHTTP(httptest.NewRecorder(), req)
	k, err := w.auth.Sitzung(context.Background(), w.mitglied.Value)
	if err != nil {
		t.Fatal(err)
	}
	if got.verein != w.a.ID || got.konto != k.ID {
		t.Fatalf("zugang verein=%q konto=%q, want %q %q", got.verein, got.konto, w.a.ID, k.ID)
	}
}

func TestRegistrierungPrueftRegelUndObjekt(t *testing.T) {
	w := &zugriffWelt{}
	z := &zugriff{rollen: &rollen{superadmins: map[string]bool{}}, team: w.team, termine: w.termine}
	panics := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: keine panic", name)
			}
		}()
		f()
	}
	leer := func(http.ResponseWriter, *http.Request, Zugang[Keins]) {}
	panics("öffentlich ohne grund", func() { neuerRouter(z).oeffentlich("GET /x", "", func(http.ResponseWriter, *http.Request) {}) })
	panics("objektregel ohne objekt", func() { neuerRouter(z).ohneObjekt("GET /x", TeamMitglied, leer) })
	panics("objekt ohne id", func() {
		mitObjekt(neuerRouter(z), "GET /x", Verwalter, z.termin(), func(http.ResponseWriter, *http.Request, Zugang[termin.Termin]) {})
	})
	panics("superadmin mit objekt", func() {
		mitObjekt(neuerRouter(z), "GET /x/{id}", Superadmin, z.termin(), func(http.ResponseWriter, *http.Request, Zugang[termin.Termin]) {})
	})
}
