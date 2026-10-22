package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/ruben-sch/teamtafel/internal/push"
)

//go:embed static
var staticFS embed.FS

// PushAbos speichert die Web-Push-Abos der Geräte eines Kontos.
type PushAbos interface {
	Speichern(ctx context.Context, kontoID string, a push.Abo) error
	Loeschen(ctx context.Context, kontoID, endpoint string) error
}

// statisch liefert Service Worker, Manifest, Skript und Icons aus.
func statisch(rt *router) {
	sub, _ := fs.Sub(staticFS, "static")
	dateien := http.FileServerFS(sub)
	datei := func(name, typ string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", typ)
			// Service Worker und Manifest immer frisch prüfen, damit Updates ankommen.
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, sub, name)
		}
	}
	rt.oeffentlich("GET /sw.js", "statische Datei", datei("sw.js", "text/javascript; charset=utf-8"))
	manifest, _ := fs.ReadFile(sub, "manifest.webmanifest")
	rt.oeffentlich("GET /manifest.webmanifest", "statische Datei", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "no-cache")
		// Auf dem Startbildschirm startet die App im Verein, von dessen Seite sie stammt.
		_, _ = w.Write(bytes.Replace(manifest, []byte(`"start_url": "/"`), []byte(`"start_url": "`+basis(r)+`/"`), 1))
	})
	rt.oeffentlich("GET /static/", "statische Dateien", http.StripPrefix("/static/", dateien).ServeHTTP)
}

type einstellungen struct {
	abos     PushAbos
	vapidKey string
	kalender Kalender
	termine  Termine
	scheme   string
}

type einstellungenSeite struct {
	Titel    string
	VAPIDKey string
	// Kalender gibt es nur im Verein.
	Kalender      bool
	KalenderAktiv bool
	// KalenderLink steht nur direkt nach dem Erneuern im Klartext zur Verfügung.
	KalenderLink string
	// KalenderWebcal ist selbst gebaut; template.URL, weil html/template webcal: sonst entschärft.
	KalenderWebcal template.URL
}

func (e *einstellungen) seite(w http.ResponseWriter, r *http.Request, z Zugang[Keins]) {
	e.zeigen(w, r, z, "")
}

func (e *einstellungen) zeigen(w http.ResponseWriter, r *http.Request, z Zugang[Keins], token string) {
	data := einstellungenSeite{Titel: "Einstellungen"}
	if e.abos != nil {
		data.VAPIDKey = e.vapidKey
	}
	if z.ImVerein && e.kalender != nil {
		data.Kalender = true
		aktiv, err := e.kalender.Vorhanden(r.Context(), z.Verein.ID, z.Konto.ID)
		if err != nil {
			interner(w, "kalender-link prüfen", err)
			return
		}
		data.KalenderAktiv = aktiv
		if token != "" {
			pfad := r.Host + basis(r) + "/kalender/" + token + ".ics"
			data.KalenderLink, data.KalenderWebcal = e.scheme+"://"+pfad, template.URL("webcal://"+pfad) // #nosec G203 -- Host und Token aus eigener Quelle
		}
	}
	render(w, r, "einstellungen.html", data)
}

// abo speichert (POST) oder löscht (DELETE) das Push-Abo des Geräts; der Body ist JSON
// von PushManager.subscribe() bzw. {"endpoint": …}.
func (e *einstellungen) abo(w http.ResponseWriter, r *http.Request, z Zugang[Keins]) {
	var a push.Abo
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&a); err != nil {
		http.Error(w, "Ungültiges Abo", http.StatusBadRequest)
		return
	}
	var err error
	if r.Method == http.MethodDelete {
		err = e.abos.Loeschen(r.Context(), z.Konto.ID, a.Endpoint)
	} else {
		err = e.abos.Speichern(r.Context(), z.Konto.ID, a)
	}
	switch {
	case errors.Is(err, push.ErrUngueltig):
		http.Error(w, "Ungültiges Abo", http.StatusBadRequest)
	case err != nil:
		interner(w, "push-abo", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
