// Package web enthält die HTTP-Handler.
package web

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/ruben-sch/teamtafel/internal/verein"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// Pinger prüft, ob die Datenbank erreichbar ist.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Vereine liefert Mandanten und ihre Mannschaften.
type Vereine interface {
	BySlug(ctx context.Context, slug string) (verein.Verein, error)
	Mannschaften(ctx context.Context, vereinID string) ([]verein.Mannschaft, error)
}

// Options sind die Abhängigkeiten des Routers.
type Options struct {
	DB      Pinger
	Vereine Vereine
	// BaseHost ist die Hauptdomain, z. B. teamtafel.schwarzpost.de.
	// Vereine liegen auf <slug>.<BaseHost>.
	BaseHost string
	Version  string
}

// NewHandler baut den Router der Anwendung.
func NewHandler(o Options) http.Handler {
	app := http.NewServeMux()
	app.HandleFunc("GET /{$}", index(o))

	root := http.NewServeMux()
	// Ohne Mandantenauflösung, weil der Docker-Healthcheck localhost aufruft.
	root.HandleFunc("GET /healthz", healthz(o.DB))
	root.Handle("/", mandant(o.BaseHost, o.Vereine, app))
	return root
}

func index(o Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := struct {
			Version      string
			Verein       *verein.Verein
			Mannschaften []verein.Mannschaft
		}{Version: o.Version}

		if v, ok := vereinAus(r.Context()); ok {
			ms, err := o.Vereine.Mannschaften(r.Context(), v.ID)
			if err != nil {
				slog.Error("mannschaften laden", "err", err, "verein", v.Slug)
				http.Error(w, "Interner Fehler", http.StatusInternalServerError)
				return
			}
			data.Verein, data.Mannschaften = &v, ms
		}
		render(w, "index.html", data)
	}
}

func render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
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
