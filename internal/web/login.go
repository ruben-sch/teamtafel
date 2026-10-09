package web

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ruben-sch/teamtafel/internal/auth"
)

const sessionCookie = "__Host-session"

type kontoKey struct{}

// kontoAus liefert das angemeldete Konto der Anfrage.
func kontoAus(ctx context.Context) (auth.Konto, bool) {
	k, ok := ctx.Value(kontoKey{}).(auth.Konto)
	return k, ok
}

// sitzung lädt das Konto aus dem Session-Cookie, falls vorhanden und gültig.
func sitzung(a Auth, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || a == nil {
			next.ServeHTTP(w, r)
			return
		}
		k, err := a.Sitzung(r.Context(), c.Value)
		if err != nil {
			if !errors.Is(err, auth.ErrUngueltig) {
				slog.Error("session prüfen", "err", err)
			}
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), kontoKey{}, k)))
	})
}

type login struct {
	auth       Auth
	mailer     Mailer
	scheme     string
	proAdresse *auth.Limiter
	proIP      *auth.Limiter
}

type loginSeite struct {
	Titel  string
	Email  string
	Fehler string
	Token  string
	// Weiter ist die Seite, auf die es nach dem Einlösen geht.
	Weiter string
}

type meldung struct {
	Titel, Text, LinkZiel, LinkText string
}

func (l *login) formular(w http.ResponseWriter, r *http.Request) {
	render(w, "login.html", loginSeite{Titel: "Anmelden", Weiter: sicheresZiel(r.URL.Query().Get("weiter"))})
}

func (l *login) anfordern(w http.ResponseWriter, r *http.Request) {
	email := auth.NormalisiereEmail(r.PostFormValue("email"))
	seite := loginSeite{Titel: "Anmelden", Email: email, Weiter: sicheresZiel(r.PostFormValue("weiter"))}

	if !l.proIP.Erlaubt(clientIP(r)) || !l.proAdresse.Erlaubt(email) {
		seite.Fehler = "Zu viele Versuche. Bitte warte ein paar Minuten."
		renderStatus(w, http.StatusTooManyRequests, "login.html", seite)
		return
	}

	token, err := l.auth.LinkAnfordern(r.Context(), email)
	if errors.Is(err, auth.ErrUngueltigeAdresse) {
		seite.Fehler = "Das sieht nicht nach einer E-Mail-Adresse aus."
		renderStatus(w, http.StatusBadRequest, "login.html", seite)
		return
	}
	if err != nil {
		slog.Error("login-link anfordern", "err", err)
		http.Error(w, "Interner Fehler", http.StatusInternalServerError)
		return
	}

	link := l.scheme + "://" + r.Host + "/auth/" + token
	if seite.Weiter != "/" {
		link += "?weiter=" + url.QueryEscape(seite.Weiter)
	}
	var text strings.Builder
	if err := mailTemplates.ExecuteTemplate(&text, "mail_login.txt", struct{ Link string }{link}); err != nil {
		slog.Error("login-mail rendern", "err", err)
		http.Error(w, "Interner Fehler", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := l.mailer.Senden(ctx, email, "Dein Anmeldelink für Teamtafel", text.String()); err != nil {
		slog.Error("login-mail senden", "err", err)
		renderStatus(w, http.StatusBadGateway, "meldung.html", meldung{
			Titel: "Mail konnte nicht verschickt werden", Text: "Bitte versuche es gleich noch einmal.",
			LinkZiel: "/login", LinkText: "Zurück zur Anmeldung",
		})
		return
	}
	render(w, "login_gesendet.html", seite)
}

// bestaetigen zeigt nur einen Knopf. Erst der POST löst den Link ein, damit
// Mail-Scanner, die Links vorab öffnen, ihn nicht verbrauchen.
func (l *login) bestaetigen(w http.ResponseWriter, r *http.Request) {
	render(w, "auth.html", loginSeite{
		Titel: "Anmelden", Token: r.PathValue("token"), Weiter: sicheresZiel(r.URL.Query().Get("weiter")),
	})
}

func (l *login) einloesen(w http.ResponseWriter, r *http.Request) {
	sess, _, err := l.auth.Einloesen(r.Context(), r.PathValue("token"))
	if errors.Is(err, auth.ErrUngueltig) {
		renderStatus(w, http.StatusBadRequest, "meldung.html", meldung{
			Titel: "Link ungültig", Text: "Der Link ist abgelaufen oder wurde schon verwendet.",
			LinkZiel: "/login", LinkText: "Neuen Link anfordern",
		})
		return
	}
	if err != nil {
		slog.Error("login-link einlösen", "err", err)
		http.Error(w, "Interner Fehler", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sess,
		Path:     "/",
		MaxAge:   int(auth.SessionGueltigkeit.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, sicheresZiel(r.FormValue("weiter")), http.StatusSeeOther)
}

// sicheresZiel lässt nur Pfade auf dem eigenen Host zu, damit "weiter" nicht
// als offene Weiterleitung auf fremde Seiten taugt.
func sicheresZiel(s string) string {
	if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/\\") {
		return "/"
	}
	u, err := url.Parse(s)
	if err != nil || u.Host != "" || u.Scheme != "" {
		return "/"
	}
	return s
}

func (l *login) abmelden(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := l.auth.Abmelden(r.Context(), c.Value); err != nil {
			slog.Error("abmelden", "err", err)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// clientIP nimmt den ersten Eintrag aus X-Forwarded-For. Die App ist nur über
// Traefik erreichbar, der den Header aus der Client-Verbindung setzt.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(ip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
