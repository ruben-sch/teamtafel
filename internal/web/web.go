// Package web enthält die HTTP-Handler.
package web

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// Pinger prüft, ob die Datenbank erreichbar ist.
type Pinger interface {
	Ping(ctx context.Context) error
}

// NewHandler baut den Router der Anwendung.
func NewHandler(db Pinger, version string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", index(version))
	mux.HandleFunc("GET /healthz", healthz(db))
	return mux
}

func index(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := templates.ExecuteTemplate(w, "index.html", struct{ Version string }{version}); err != nil {
			slog.Error("startseite rendern", "err", err)
		}
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
