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
	templates     = template.Must(template.ParseFS(templateFS, "templates/*.html"))
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
}

// Auth verwaltet Login-Links und Sessions.
type Auth interface {
	LinkAnfordern(ctx context.Context, email string) (string, error)
	Einloesen(ctx context.Context, token string) (string, auth.Konto, error)
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
	l := &login{
		auth:       o.Auth,
		mailer:     o.Mailer,
		scheme:     o.Scheme,
		proAdresse: auth.NewLimiter(5, 15*time.Minute),
		proIP:      auth.NewLimiter(20, 15*time.Minute),
	}

	app := http.NewServeMux()
	app.HandleFunc("GET /{$}", index(o))
	app.HandleFunc("GET /login", l.formular)
	app.HandleFunc("POST /login", l.anfordern)
	app.HandleFunc("GET /auth/{token}", l.bestaetigen)
	app.HandleFunc("POST /auth/{token}", l.einloesen)
	app.HandleFunc("POST /logout", l.abmelden)
	if o.Team != nil {
		t := &teamSeiten{team: o.Team, scheme: o.Scheme}
		app.HandleFunc("GET /m/{id}", t.mannschaft)
		app.HandleFunc("POST /m/{id}/einladung", t.einladung)
		app.HandleFunc("GET /join/{token}", t.joinFormular)
		app.HandleFunc("POST /join/{token}", t.joinAnfragen)
		app.HandleFunc("POST /anfragen/{id}/freigeben", t.anfrageEntscheiden(t.freigeben))
		app.HandleFunc("POST /anfragen/{id}/ablehnen", t.anfrageEntscheiden(t.ablehnen))
	}

	root := http.NewServeMux()
	// Ohne Mandantenauflösung, weil der Docker-Healthcheck localhost aufruft.
	root.HandleFunc("GET /healthz", healthz(o.DB))
	root.Handle("/", mandant(o.BaseHost, o.Vereine, sitzung(o.Auth, app)))

	// CSRF: Schreibende Requests von fremden Origins lehnt die Standardbibliothek
	// anhand von Sec-Fetch-Site bzw. Origin ab; dazu kommt SameSite=Lax am Cookie.
	return http.NewCrossOriginProtection().Handler(root)
}

func index(o Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := struct {
			Titel        string
			Version      string
			Verein       *verein.Verein
			Mannschaften []verein.Mannschaft
			Trainer      []verein.Mannschaft
			Spieler      []team.Spieler
			Konto        *auth.Konto
		}{Version: o.Version}
		if k, ok := kontoAus(r.Context()); ok {
			data.Konto = &k
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
