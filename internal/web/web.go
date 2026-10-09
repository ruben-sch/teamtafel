// Package web enthält die HTTP-Handler.
package web

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	texttemplate "text/template"
	"time"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

//go:embed templates/*.html templates/*.txt
var templateFS embed.FS

var (
	templates     = template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html"))
	mailTemplates = texttemplate.Must(texttemplate.ParseFS(templateFS, "templates/*.txt"))
)

// Pinger prüft, ob die Datenbank erreichbar ist.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Vereine liefert Mandanten und ihre Mannschaften.
type Vereine interface {
	BySlug(ctx context.Context, slug string) (verein.Verein, error)
	Mannschaften(ctx context.Context, vereinID string) ([]verein.Mannschaft, error)
	Alle(ctx context.Context) ([]verein.Verein, error)
	Anlegen(ctx context.Context, slug, name string) (verein.Verein, error)
	MannschaftAnlegen(ctx context.Context, vereinID, saison, name string) (verein.Mannschaft, error)
	AdminHinzufuegen(ctx context.Context, vereinID, kontoID string) error
	IstAdmin(ctx context.Context, vereinID, kontoID string) (bool, error)
	Admins(ctx context.Context, vereinID string) ([]string, error)
}

// Auth verwaltet Login-Links und Sessions.
type Auth interface {
	LinkAnfordern(ctx context.Context, email string) (string, error)
	Einloesen(ctx context.Context, token string) (string, auth.Konto, error)
	KontoFuer(ctx context.Context, email string) (auth.Konto, error)
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
	// BaseHost ist die Hauptdomain, z. B. teamtafel.schwarzpost.de.
	// Vereine liegen auf <slug>.<BaseHost>.
	BaseHost string
	Version  string
}

// NewHandler baut den Router der Anwendung.
func NewHandler(o Options) http.Handler {
	if o.Scheme == "" {
		o.Scheme = "https"
	}
	rollen := &rollen{vereine: o.Vereine, superadmins: map[string]bool{}}
	for _, e := range o.Superadmins {
		if e = auth.NormalisiereEmail(e); e != "" {
			rollen.superadmins[e] = true
		}
	}
	l := &login{
		auth:       o.Auth,
		mailer:     o.Mailer,
		scheme:     o.Scheme,
		proAdresse: auth.NewLimiter(5, 15*time.Minute),
		proIP:      auth.NewLimiter(20, 15*time.Minute),
	}

	app := http.NewServeMux()
	app.HandleFunc("GET /{$}", index(o, rollen))
	app.HandleFunc("GET /login", l.formular)
	app.HandleFunc("POST /login", l.anfordern)
	app.HandleFunc("GET /auth/{token}", l.bestaetigen)
	app.HandleFunc("POST /auth/{token}", l.einloesen)
	app.HandleFunc("POST /logout", l.abmelden)
	statisch(app)
	e := &einstellungen{vapidKey: o.VAPIDPublicKey, scheme: o.Scheme}
	if o.Kalender != nil && o.Termine != nil {
		e.kalender, e.termine = o.Kalender, o.Termine
		app.HandleFunc("POST /einstellungen/kalender", e.kalenderErneuern)
		app.HandleFunc("GET /kalender/{datei}", e.kalenderFeed)
	}
	if o.Push != nil && o.VAPIDPublicKey != "" {
		e.abos = o.Push
		app.HandleFunc("POST /push/abo", e.abo)
		app.HandleFunc("DELETE /push/abo", e.abo)
	}
	app.HandleFunc("GET /einstellungen", e.seite)
	if o.Team != nil {
		t := &teamSeiten{team: o.Team, termine: o.Termine, rollen: rollen, scheme: o.Scheme}
		app.HandleFunc("GET /m/{id}", t.mannschaft)
		app.HandleFunc("POST /m/{id}/einladung", t.einladung)
		app.HandleFunc("GET /join/{token}", t.joinFormular)
		app.HandleFunc("POST /join/{token}", t.joinAnfragen)
		app.HandleFunc("POST /anfragen/{id}/freigeben", t.anfrageEntscheiden(t.freigeben))
		app.HandleFunc("POST /anfragen/{id}/ablehnen", t.anfrageEntscheiden(t.ablehnen))

		if o.Termine != nil {
			app.HandleFunc("GET /m/{id}/termine/neu", t.terminNeu)
			app.HandleFunc("POST /m/{id}/termine", t.terminAnlegen)
			app.HandleFunc("POST /m/{id}/serien", t.serieAnlegen)
			app.HandleFunc("POST /serien/{id}/beenden", t.serieBeenden)
			app.HandleFunc("GET /t/{id}", t.terminDetail)
			app.HandleFunc("POST /t/{id}", t.terminAendern)
			app.HandleFunc("POST /t/{id}/absagen", t.terminAbsagen)
			app.HandleFunc("POST /t/{id}/rueckmeldung/{spieler}", t.rueckmelden)
		}

		a := &verwaltung{vereine: o.Vereine, team: o.Team, auth: o.Auth, rollen: rollen, scheme: o.Scheme}
		app.HandleFunc("GET /admin", a.seite)
		app.HandleFunc("POST /admin/vereine", a.vereinAnlegen)
		app.HandleFunc("POST /admin/admins", a.adminHinzufuegen)
		app.HandleFunc("POST /admin/mannschaften", a.mannschaftAnlegen)
		app.HandleFunc("POST /admin/mannschaften/{id}/trainer", a.trainerHinzufuegen)
		app.HandleFunc("POST /admin/mannschaften/{id}/trainer/{konto}/entfernen", a.trainerEntfernen)
	}

	root := http.NewServeMux()
	// Ohne Mandantenauflösung, weil der Docker-Healthcheck localhost aufruft.
	root.HandleFunc("GET /healthz", healthz(o.DB))
	root.Handle("/", mandant(o.BaseHost, o.Vereine, sitzung(o.Auth, app)))

	// CSRF: Schreibende Requests von fremden Origins lehnt die Standardbibliothek
	// anhand von Sec-Fetch-Site bzw. Origin ab; dazu kommt SameSite=Lax am Cookie.
	return http.NewCrossOriginProtection().Handler(root)
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
		render(w, "index.html", data)
	}
}

func render(w http.ResponseWriter, name string, data any) {
	renderStatus(w, http.StatusOK, name, data)
}

func renderStatus(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := templates.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("template rendern", "template", name, "err", err)
	}
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
