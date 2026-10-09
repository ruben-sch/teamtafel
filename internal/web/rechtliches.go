package web

import "net/http"

// Betreiber sind die Angaben für Impressum und Datenschutzerklärung.
type Betreiber struct {
	Name, Anschrift, Email string
}

// Vollstaendig meldet, ob alle Pflichtangaben gesetzt sind.
func (b Betreiber) Vollstaendig() bool {
	return b.Name != "" && b.Anschrift != "" && b.Email != ""
}

type rechtlichesSeite struct {
	Titel     string
	Betreiber Betreiber
}

// rechtliches liefert Impressum oder Datenschutzerklärung; beide sind ohne Anmeldung erreichbar.
func rechtliches(b Betreiber, name, titel string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		render(w, name, rechtlichesSeite{Titel: titel, Betreiber: b})
	}
}
