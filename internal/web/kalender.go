package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ruben-sch/teamtafel/internal/kalender"
)

// Kalender verwaltet die geheimen iCal-Links je Konto und Verein.
type Kalender interface {
	Erneuern(ctx context.Context, vereinID, kontoID string) (string, error)
	Vorhanden(ctx context.Context, vereinID, kontoID string) (bool, error)
	Konto(ctx context.Context, vereinID, token string) (string, error)
}

// kalenderErneuern erzeugt einen neuen Link und zeigt ihn einmalig an.
func (e *einstellungen) kalenderErneuern(w http.ResponseWriter, r *http.Request) {
	v, k, ok := angemeldet(w, r)
	if !ok {
		return
	}
	token, err := e.kalender.Erneuern(r.Context(), v.ID, k.ID)
	if err != nil {
		interner(w, "kalender-link erneuern", err)
		return
	}
	e.zeigen(w, r, token)
}

// kalenderFeed liefert die Termine des Kontos von vier Wochen zurück bis ein Jahr voraus.
// Der Link ersetzt das Login, weil Kalender-Apps keine Sitzung haben.
func (e *einstellungen) kalenderFeed(w http.ResponseWriter, r *http.Request) {
	v, ok := vereinAus(r.Context())
	token, ics := strings.CutSuffix(r.PathValue("datei"), ".ics")
	if !ok || !ics {
		http.NotFound(w, r)
		return
	}
	kontoID, err := e.kalender.Konto(r.Context(), v.ID, token)
	if errors.Is(err, kalender.ErrUnbekannt) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		interner(w, "kalender-link prüfen", err)
		return
	}
	ab := heute().AddDate(0, 0, -28)
	ts, err := e.termine.FuerKonto(r.Context(), v.ID, kontoID, ab, ab.AddDate(1, 1, 0))
	if err != nil {
		interner(w, "kalender laden", err)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=300")
	kalender.ICS(w, kalender.Kopf{Name: v.Name, Host: r.Host, Scheme: e.scheme}, ts, time.Now())
}
