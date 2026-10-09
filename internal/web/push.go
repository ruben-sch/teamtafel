package web

import (
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
func statisch(app *http.ServeMux) {
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
	app.HandleFunc("GET /sw.js", datei("sw.js", "text/javascript; charset=utf-8"))
	app.HandleFunc("GET /manifest.webmanifest", datei("manifest.webmanifest", "application/manifest+json"))
	app.Handle("GET /static/", http.StripPrefix("/static/", dateien))
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
	// Kalender gibt es nur auf einer Vereins-Subdomain.
	Kalender      bool
	KalenderAktiv bool
	// KalenderLink steht nur direkt nach dem Erneuern im Klartext zur Verfügung.
	KalenderLink string
	// KalenderWebcal ist selbst gebaut; template.URL, weil html/template webcal: sonst entschärft.
	KalenderWebcal template.URL
}

func (e *einstellungen) seite(w http.ResponseWriter, r *http.Request) {
	e.zeigen(w, r, "")
}

func (e *einstellungen) zeigen(w http.ResponseWriter, r *http.Request, token string) {
	k, ok := kontoAus(r.Context())
	if !ok {
		http.Redirect(w, r, "/login?weiter=/einstellungen", http.StatusSeeOther)
		return
	}
	data := einstellungenSeite{Titel: "Einstellungen"}
	if e.abos != nil {
		data.VAPIDKey = e.vapidKey
	}
	if v, ok := vereinAus(r.Context()); ok && e.kalender != nil {
		data.Kalender = true
		aktiv, err := e.kalender.Vorhanden(r.Context(), v.ID, k.ID)
		if err != nil {
			interner(w, "kalender-link prüfen", err)
			return
		}
		data.KalenderAktiv = aktiv
		if token != "" {
			pfad := r.Host + "/kalender/" + token + ".ics"
			data.KalenderLink, data.KalenderWebcal = e.scheme+"://"+pfad, template.URL("webcal://"+pfad) // #nosec G203 -- Host und Token aus eigener Quelle
		}
	}
	render(w, "einstellungen.html", data)
}

// abo speichert (POST) oder löscht (DELETE) das Push-Abo des Geräts; der Body ist JSON
// von PushManager.subscribe() bzw. {"endpoint": …}.
func (e *einstellungen) abo(w http.ResponseWriter, r *http.Request) {
	k, ok := kontoAus(r.Context())
	if !ok {
		http.Error(w, "Nicht angemeldet", http.StatusUnauthorized)
		return
	}
	var a push.Abo
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&a); err != nil {
		http.Error(w, "Ungültiges Abo", http.StatusBadRequest)
		return
	}
	var err error
	if r.Method == http.MethodDelete {
		err = e.abos.Loeschen(r.Context(), k.ID, a.Endpoint)
	} else {
		err = e.abos.Speichern(r.Context(), k.ID, a)
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
