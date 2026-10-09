package web

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/ruben-sch/teamtafel/internal/verein"
)

type vereinKey struct{}

// vereinAus liefert den Verein der Anfrage; ok ist false auf der Hauptdomain.
func vereinAus(ctx context.Context) (verein.Verein, bool) {
	v, ok := ctx.Value(vereinKey{}).(verein.Verein)
	return v, ok
}

// mandant ermittelt den Verein aus der Subdomain. Die Hauptdomain läuft ohne
// Verein weiter, unbekannte Vereine und fremde Hosts enden mit 404.
func mandant(baseHost string, vereine Vereine, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host == baseHost {
			next.ServeHTTP(w, r)
			return
		}
		slug, ok := strings.CutSuffix(host, "."+baseHost)
		if !ok || slug == "" || strings.Contains(slug, ".") {
			http.NotFound(w, r)
			return
		}
		v, err := vereine.BySlug(r.Context(), slug)
		if errors.Is(err, verein.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			slog.Error("verein auflösen", "err", err, "slug", slug)
			http.Error(w, "Interner Fehler", http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), vereinKey{}, v)))
	})
}
