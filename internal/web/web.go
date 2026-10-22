// Package web enthält die HTTP-Handler.
package web

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	texttemplate "text/template"
	"time"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

//go:embed templates/*.html templates/*.txt
var templateFS embed.FS

var (
	// templates wird nie selbst ausgeführt, sondern je Vereinspfad geklont (vorlagen).
	templates = template.Must(template.New("").Funcs(funcs).Funcs(template.FuncMap{
		"basis": func() string { return "" },
	}).ParseFS(templateFS, "templates/*.html"))
	mailTemplates = texttemplate.Must(texttemplate.ParseFS(templateFS, "templates/*.txt"))
	// geklont hält je Vereinspfad die Templates mit passendem basis.
	geklont sync.Map
)

// vorlagen liefert die Templates, in denen {{basis}} den Vereinspfad b liefert.
func vorlagen(b string) *template.Template {
	if t, ok := geklont.Load(b); ok {
		return t.(*template.Template)
	}
	t := template.Must(templates.Clone()).Funcs(template.FuncMap{"basis": func() string { return b }})
	gespeichert, _ := geklont.LoadOrStore(b, t)
	return gespeichert.(*template.Template)
}

// Pinger prüft, ob die Datenbank erreichbar ist.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Vereine liefert Mandanten und ihre Mannschaften.
type Vereine interface {
	BySlug(ctx context.Context, slug string) (verein.Verein, error)
	Mannschaften(ctx context.Context, vereinID string) ([]verein.Mannschaft, error)
	Alle(ctx context.Context) ([]verein.Verein, error)
	AnlegenAusName(ctx context.Context, name string) (verein.Verein, error)
	MannschaftAnlegen(ctx context.Context, vereinID, saison, name string) (verein.Mannschaft, error)
	AdminHinzufuegen(ctx context.Context, vereinID, kontoID string) error
	IstAdmin(ctx context.Context, vereinID, kontoID string) (bool, error)
	Admins(ctx context.Context, vereinID string) ([]string, error)
	FarbeSetzen(ctx context.Context, vereinID, farbe string) error
}

// Auth verwaltet Login-Links und Sessions.
type Auth interface {
	LinkAnfordern(ctx context.Context, email string) (string, error)
	Einloesen(ctx context.Context, token string) (string, auth.Konto, error)
	KontoFuer(ctx context.Context, email string) (auth.Konto, error)
	KontoBekannt(ctx context.Context, email string) (bool, error)
	Sitzung(ctx context.Context, token string) (auth.Konto, error)
	Abmelden(ctx context.Context, token string) error
}

// Mailer verschickt Text-Mails.
type Mailer interface {
	Senden(ctx context.Context, an, betreff, text string) error
}

// Options sind die Abhängigkeiten des Routers.
type Options struct {
	DB      Pinger
	Vereine Vereine
	Auth    Auth
	Mailer  Mailer
	// Team ist optional; ohne fehlen die Mannschafts- und Beitrittsseiten.
	Team Team
	// Termine ist optional; ohne fehlen die Terminseiten.
	Termine Termine
	// Push ist optional; ohne Abo-Speicher oder VAPIDPublicKey gibt es nur E-Mails.
	Push           PushAbos
	VAPIDPublicKey string
	// Kalender ist optional; ohne gibt es kein iCal-Abo.
	Kalender Kalender
	// Superadmins sind die E-Mail-Adressen der Plattform-Admins.
	Superadmins []string
	// Scheme für Links in Mails: "https", lokal "http".
	Scheme string
	// BaseHost ist die Domain, z. B. teamtafel.schwarzpost.de.
	// Vereine liegen auf <BaseHost>/<slug>/.
	BaseHost string
	Version  string
	// Betreiber erscheint in Impressum und Datenschutzerklärung.
	Betreiber Betreiber
}

// NewHandler baut den Router der Anwendung.
func NewHandler(o Options) http.Handler {
	o = mitVorgaben(o)
	root := http.NewServeMux()
	// Ohne Mandantenauflösung, weil der Docker-Healthcheck localhost aufruft.
	root.HandleFunc("GET /healthz", healthz(o.DB))
	kn := &kennungen{rollen: neueRollen(o), team: o.Team}
	root.Handle("/", mandant(o.Scheme, o.BaseHost, o.Vereine, sitzung(o.Auth, mitKennung(kn, routenVon(o)))))

	// CSRF: Schreibende Requests von fremden Origins lehnt die Standardbibliothek
	// anhand von Sec-Fetch-Site bzw. Origin ab; dazu kommt SameSite=Lax am Cookie.
	return http.NewCrossOriginProtection().Handler(root)
}

func mitVorgaben(o Options) Options {
	if o.Scheme == "" {
		o.Scheme = "https"
	}
	return o
}

func neueRollen(o Options) *rollen {
	rl := &rollen{vereine: o.Vereine, superadmins: map[string]bool{}}
	for _, e := range o.Superadmins {
		if e = auth.NormalisiereEmail(e); e != "" {
			rl.superadmins[e] = true
		}
	}
	return rl
}

// routenVon registriert jede Route der Anwendung mit ihrer Regel im Zugriffsmodul.
func routenVon(o Options) *router {
	o = mitVorgaben(o)
	rollen := neueRollen(o)
	z := &zugriff{rollen: rollen, team: o.Team, termine: o.Termine}
	rt := neuerRouter(z)
	l := &login{
		auth:       o.Auth,
		mailer:     o.Mailer,
		scheme:     o.Scheme,
		rollen:     rollen,
		team:       o.Team,
		proAdresse: auth.NewLimiter(5, 15*time.Minute),
		proIP:      auth.NewLimiter(20, 15*time.Minute),
	}

	rt.oeffentlich("GET /{$}", "Startseite, angemeldet mit mehr Inhalt", index(o, rollen))
	rt.oeffentlich("GET /login", "Anmeldung", l.formular)
	rt.oeffentlich("POST /login", "Anmeldung", l.anfordern)
	rt.oeffentlich("GET /auth/{token}", "Magic Link", l.bestaetigen)
	rt.oeffentlich("POST /auth/{token}", "Magic Link", l.einloesen)
	rt.oeffentlich("POST /logout", "Abmeldung", l.abmelden)
	statisch(rt)
	rt.oeffentlich("GET /verein.css", "Vereinsfarbe", vereinCSS)
	rt.oeffentlich("GET /impressum", "Pflichtangabe", rechtliches(o.Betreiber, "impressum.html", "Impressum"))
	rt.oeffentlich("GET /datenschutz", "Pflichtangabe", rechtliches(o.Betreiber, "datenschutz.html", "Datenschutz"))
	e := &einstellungen{vapidKey: o.VAPIDPublicKey, scheme: o.Scheme}
	if o.Kalender != nil && o.Termine != nil {
		e.kalender, e.termine = o.Kalender, o.Termine
		rt.ohneObjekt("POST /einstellungen/kalender", Angemeldet, e.kalenderErneuern)
		rt.oeffentlich("GET /kalender/{datei}", "geheimer Link ersetzt das Login", e.kalenderFeed)
	}
	if o.Push != nil && o.VAPIDPublicKey != "" {
		e.abos = o.Push
		rt.ohneObjekt("POST /push/abo", KontoAPI, e.abo)
		rt.ohneObjekt("DELETE /push/abo", KontoAPI, e.abo)
	}
	rt.ohneObjekt("GET /einstellungen", Konto, e.seite)
	if o.Team != nil {
		t := &teamSeiten{team: o.Team, termine: o.Termine, scheme: o.Scheme}
		mannschaft := z.mannschaft()
		mitObjekt(rt, "GET /m/{id}", Verwalter, mannschaft, t.mannschaft)
		mitObjekt(rt, "POST /m/{id}/einladung", Verwalter, mannschaft, t.einladung)
		rt.ohneObjekt("GET /join/{token}", Angemeldet, t.joinFormular)
		rt.ohneObjekt("POST /join/{token}", Angemeldet, t.joinAnfragen)
		mitObjekt(rt, "POST /anfragen/{id}/freigeben", Trainer, z.anfrage(), t.anfrageEntscheiden(t.freigeben))
		mitObjekt(rt, "POST /anfragen/{id}/ablehnen", Trainer, z.anfrage(), t.anfrageEntscheiden(t.ablehnen))

		if o.Termine != nil {
			tm := z.termin()
			aendern := Verwalter.Text("Termine ändern nur die Trainer.")
			mitObjekt(rt, "GET /m/{id}/termine/neu", Verwalter, mannschaft, t.terminNeu)
			mitObjekt(rt, "POST /m/{id}/termine", Verwalter, mannschaft, t.terminAnlegen)
			mitObjekt(rt, "POST /m/{id}/serien", Verwalter, mannschaft, t.serieAnlegen)
			mitObjekt(rt, "POST /serien/{id}/beenden", Verwalter, z.serie(), t.serieBeenden)
			// Nur Trainer, nicht Vereinsadmins: Die Zahlen sind für die Arbeit mit der Mannschaft gedacht.
			mitObjekt(rt, "GET /m/{id}/statistik", Trainer, mannschaft, t.statistik)
			mitObjekt(rt, "GET /t/{id}", TeamMitglied, tm, t.terminDetail)
			mitObjekt(rt, "POST /t/{id}", aendern, tm, t.terminAendern)
			mitObjekt(rt, "POST /t/{id}/absagen", aendern, tm, t.terminAbsagen)
			mitObjekt(rt, "POST /t/{id}/rueckmeldung/{spieler}", TeamMitglied, tm, t.rueckmelden)
			mitObjekt(rt, "POST /t/{id}/anwesenheit/{spieler}", Verwalter.Text("Die Anwesenheit tragen nur die Trainer ein."), tm, t.anwesenheitSetzen)
			mitObjekt(rt, "POST /t/{id}/dienste", aendern, tm, t.dienstAnlegen)
			mitObjekt(rt, "POST /t/{id}/dienste/{dienst}", aendern, tm, t.dienstAendern)
			mitObjekt(rt, "POST /t/{id}/dienste/{dienst}/loeschen", aendern, tm, t.dienstLoeschen)
			mitObjekt(rt, "POST /t/{id}/dienste/{dienst}/eintragen", TeamMitglied, tm, t.dienstEintrag(true))
			mitObjekt(rt, "POST /t/{id}/dienste/{dienst}/austragen", TeamMitglied, tm, t.dienstEintrag(false))
		}

		a := &verwaltung{vereine: o.Vereine, team: o.Team, auth: o.Auth, scheme: o.Scheme}
		rt.ohneObjekt("GET /admin", Admin, a.seite)
		rt.ohneObjekt("POST /admin/vereine", Superadmin, a.vereinAnlegen)
		rt.ohneObjekt("POST /admin/admins", Vereinsadmin, a.adminHinzufuegen)
		rt.ohneObjekt("POST /admin/farbe", Vereinsadmin, a.farbeSetzen)
		rt.ohneObjekt("POST /admin/mannschaften", Vereinsadmin, a.mannschaftAnlegen)
		mitObjekt(rt, "POST /admin/mannschaften/{id}/trainer", Vereinsadmin, mannschaft, a.trainerHinzufuegen)
		mitObjekt(rt, "POST /admin/mannschaften/{id}/trainer/{konto}/entfernen", Vereinsadmin, mannschaft, a.trainerEntfernen)
	}
	return rt
}

func index(o Options, rl *rollen) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := struct {
			Titel        string
			Version      string
			Verein       *verein.Verein
			Mannschaften []verein.Mannschaft
			Trainer      []verein.Mannschaft
			Spieler      []team.Spieler
			Termine      []terminKarte
			Konto        *auth.Konto
			Admin        bool
		}{Version: o.Version}
		if k, ok := kontoAus(r.Context()); ok {
			data.Konto = &k
			admin, err := rl.darfVerwalten(r.Context(), k)
			if err != nil {
				interner(w, "adminrolle prüfen", err)
				return
			}
			data.Admin = admin && o.Team != nil
		}

		if v, ok := vereinAus(r.Context()); ok {
			ms, err := o.Vereine.Mannschaften(r.Context(), v.ID)
			if err != nil {
				slog.Error("mannschaften laden", "err", err, "verein", v.Slug)
				http.Error(w, "Interner Fehler", http.StatusInternalServerError)
				return
			}
			data.Verein, data.Mannschaften, data.Titel = &v, ms, v.Name
			if data.Konto != nil && o.Team != nil {
				if data.Trainer, err = o.Team.TrainerMannschaften(r.Context(), v.ID, data.Konto.ID); err != nil {
					interner(w, "trainer-mannschaften laden", err)
					return
				}
				if data.Spieler, err = o.Team.MeineSpieler(r.Context(), v.ID, data.Konto.ID); err != nil {
					interner(w, "eigene spieler laden", err)
					return
				}
				if o.Termine != nil {
					ab := heute()
					ts, err := o.Termine.FuerKonto(r.Context(), v.ID, data.Konto.ID, ab, ab.AddDate(0, 0, 28))
					if err == nil {
						data.Termine, err = karten(r.Context(), o.Termine, v.ID, data.Konto.ID, ts, false)
					}
					if err != nil {
						interner(w, "termine laden", err)
						return
					}
				}
			}
		}
		render(w, r, "index.html", data)
	}
}

// vereinCSS liefert die Vereinsfarbe als CSS-Variable. Eine eigene Datei statt
// Inline-Style, damit nicht jede Seite den Verein in ihre Daten aufnehmen muss.
func vereinCSS(w http.ResponseWriter, r *http.Request) {
	farbe := verein.StandardFarbe
	if v, ok := vereinAus(r.Context()); ok && v.Farbe != "" {
		farbe = v.Farbe
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	// Immer frisch prüfen, damit eine neue Farbe sofort ankommt.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", `"`+strings.TrimPrefix(farbe, "#")+`"`)
	if r.Header.Get("If-None-Match") == w.Header().Get("ETag") {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write([]byte(":root { --verein: " + farbe + "; }\n"))
}

func render(w http.ResponseWriter, r *http.Request, name string, data any) {
	renderStatus(w, r, http.StatusOK, name, data)
}

func renderStatus(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	t := vorlagen(basis(r))
	kopf := kopfDaten{Titel: titelAus(data), Kennung: kennungAus(r.Context())}
	if err := t.ExecuteTemplate(w, "kopf", kopf); err != nil {
		slog.Error("template rendern", "template", "kopf", "err", err)
		return
	}
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("template rendern", "template", name, "err", err)
	}
}

// umleiten schickt nach dem Absenden auf pfad im Verein der Anfrage.
func umleiten(w http.ResponseWriter, r *http.Request, pfad string) {
	http.Redirect(w, r, basis(r)+pfad, http.StatusSeeOther)
}

func healthz(db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err := db.Ping(ctx); err != nil {
			slog.Warn("healthcheck: datenbank nicht erreichbar", "err", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("datenbank nicht erreichbar\n"))
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	}
}
