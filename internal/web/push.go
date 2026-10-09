package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
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
}

func (e *einstellungen) seite(w http.ResponseWriter, r *http.Request) {
	if _, ok := kontoAus(r.Context()); !ok {
		http.Redirect(w, r, "/login?weiter=/einstellungen", http.StatusSeeOther)
		return
	}
	data := struct {
		Titel    string
		VAPIDKey string
	}{Titel: "Einstellungen"}
	if e.abos != nil {
		data.VAPIDKey = e.vapidKey
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
