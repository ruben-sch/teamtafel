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

// vereinAus liefert den Verein der Anfrage; ok ist false außerhalb eines Vereinspfads.
func vereinAus(ctx context.Context) (verein.Verein, bool) {
	v, ok := ctx.Value(vereinKey{}).(verein.Verein)
	return v, ok
}

// basis ist der Pfad des Vereins, z. B. "/fc"; ohne Verein leer. Links und
// Weiterleitungen innerhalb der Anwendung beginnen damit.
func basis(r *http.Request) string {
	if v, ok := vereinAus(r.Context()); ok {
		return "/" + v.Slug
	}
	return ""
}

// mandant ermittelt den Verein aus dem ersten Pfadsegment (<baseHost>/<slug>/…) und
// reicht die Anfrage ohne dieses Segment weiter. Pfade ohne Verein laufen ohne
// Verein weiter. Alte Links auf <slug>.<baseHost> leitet es dauerhaft auf den Pfad
// um; fremde Hosts enden mit 404.
func mandant(scheme, baseHost string, vereine Vereine, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != baseHost {
			subdomainUmleiten(w, r, scheme, baseHost, host)
			return
		}

		slug, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if !verein.SlugGueltig(slug) {
			next.ServeHTTP(w, r)
			return
		}
		v, err := vereine.BySlug(r.Context(), slug)
		if errors.Is(err, verein.ErrNotFound) {
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			slog.Error("verein auflösen", "err", err, "slug", slug)
			http.Error(w, "Interner Fehler", http.StatusInternalServerError)
			return
		}
		if rest == "" && !strings.HasSuffix(r.URL.Path, "/") {
			ziel := "/" + slug + "/"
			if r.URL.RawQuery != "" {
				ziel += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, ziel, http.StatusMovedPermanently)
			return
		}
		ctx := context.WithValue(r.Context(), vereinKey{}, v)
		http.StripPrefix("/"+slug, next).ServeHTTP(w, r.WithContext(ctx))
	})
}

// subdomainUmleiten schickt <slug>.<baseHost>/pfad nach <baseHost>/<slug>/pfad,
// damit Team-Links, Kalender-Abos und Mails aus der Zeit der Subdomains weiter gehen.
func subdomainUmleiten(w http.ResponseWriter, r *http.Request, scheme, baseHost, host string) {
	slug, ok := strings.CutSuffix(host, "."+baseHost)
	if !ok || !verein.SlugGueltig(slug) {
		http.NotFound(w, r)
		return
	}
	ziel := scheme + "://" + strings.TrimPrefix(strings.ToLower(r.Host), slug+".") + "/" + slug + r.URL.RequestURI()
	status := http.StatusPermanentRedirect
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		status = http.StatusMovedPermanently
	}
	http.Redirect(w, r, ziel, status)
}
