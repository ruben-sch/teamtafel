package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ruben-sch/teamtafel/internal/termin"
)

// dienstAnsicht ist ein Dienst auf der Terminseite.
type dienstAnsicht struct {
	termin.Dienst
	// Meins: Das angemeldete Konto ist eingetragen.
	Meins bool
	// Einteilbar sind die Konten der Mannschaft, die noch nicht eingetragen sind (nur für Trainer).
	Einteilbar []termin.Person
}

const dienstFehler = "Bitte eine Bezeichnung (höchstens 80 Zeichen) und 1 bis 20 Plätze angeben, nicht weniger als schon eingetragen sind."

func dienstFormLesen(r *http.Request) (string, int) {
	plaetze, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("plaetze")))
	if err != nil {
		plaetze = 0
	}
	return r.PostFormValue("bezeichnung"), plaetze
}

// dienstErgebnis leitet zurück zum Termin oder zeigt Fehler: ungültige Angaben auf der Terminseite.
func (s *teamSeiten) dienstErgebnis(w http.ResponseWriter, r *http.Request, t termin.Termin, err error, was string) {
	v, _ := vereinAus(r.Context())
	switch {
	case errors.Is(err, termin.ErrUngueltig):
		s.terminSeiteZeigen(w, r, v, http.StatusBadRequest, terminSeite{Termin: t, Verwalter: true,
			Form: terminFormAus(t.Daten), DienstFehler: dienstFehler})
	case errors.Is(err, termin.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		interner(w, was, err)
	default:
		umleiten(w, r, "/t/"+t.ID)
	}
}

func (s *teamSeiten) dienstAnlegen(w http.ResponseWriter, r *http.Request) {
	v, t, ok := s.verwalterTermin(w, r)
	if !ok {
		return
	}
	bezeichnung, plaetze := dienstFormLesen(r)
	_, err := s.termine.DienstAnlegen(r.Context(), v.ID, t.ID, bezeichnung, plaetze)
	s.dienstErgebnis(w, r, t, err, "dienst anlegen")
}

// dienstID liefert die Dienst-ID aus dem Pfad oder antwortet mit 404.
func dienstID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("dienst")
	if !uuidPattern.MatchString(id) {
		http.NotFound(w, r)
		return "", false
	}
	return id, true
}

func (s *teamSeiten) dienstAendern(w http.ResponseWriter, r *http.Request) {
	v, t, ok := s.verwalterTermin(w, r)
	if !ok {
		return
	}
	id, ok := dienstID(w, r)
	if !ok {
		return
	}
	bezeichnung, plaetze := dienstFormLesen(r)
	s.dienstErgebnis(w, r, t, s.termine.DienstAendern(r.Context(), v.ID, t.ID, id, bezeichnung, plaetze), "dienst ändern")
}

func (s *teamSeiten) dienstLoeschen(w http.ResponseWriter, r *http.Request) {
	v, t, ok := s.verwalterTermin(w, r)
	if !ok {
		return
	}
	id, ok := dienstID(w, r)
	if !ok {
		return
	}
	s.dienstErgebnis(w, r, t, s.termine.DienstLoeschen(r.Context(), v.ID, t.ID, id), "dienst löschen")
}

// dienstEintrag trägt das eigene Konto ein oder aus; mit Formularfeld konto ein anderes,
// das dürfen nur Trainer und Vereinsadmins.
func (s *teamSeiten) dienstEintrag(eintragen bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, t, verwalter, ok := s.terminLaden(w, r)
		if !ok {
			return
		}
		id, ok := dienstID(w, r)
		if !ok {
			return
		}
		k, _ := kontoAus(r.Context())
		konto := r.PostFormValue("konto")
		if konto == "" {
			konto = k.ID
		}
		zurueck := "/t/" + t.ID
		if konto != k.ID && (!verwalter || !uuidPattern.MatchString(konto)) {
			renderStatus(w, r, http.StatusForbidden, "meldung.html", meldung{
				Titel: "Kein Zugriff", Text: "Andere ein- oder austragen können nur die Trainer.", LinkZiel: zurueck, LinkText: "Zurück zum Termin",
			})
			return
		}
		var err error
		if eintragen {
			err = s.termine.Eintragen(r.Context(), v.ID, t.ID, id, konto, k.ID)
		} else {
			err = s.termine.Austragen(r.Context(), v.ID, t.ID, id, konto, k.ID)
		}
		switch {
		case errors.Is(err, termin.ErrNotFound):
			http.NotFound(w, r)
		case errors.Is(err, termin.ErrNichtBerechtigt):
			renderStatus(w, r, http.StatusForbidden, "meldung.html", meldung{
				Titel: "Kein Zugriff", Text: "Dienste übernehmen kann nur, wer zur Mannschaft gehört.", LinkZiel: zurueck, LinkText: "Zurück zum Termin",
			})
		case errors.Is(err, termin.ErrVoll), errors.Is(err, termin.ErrFristVorbei), errors.Is(err, termin.ErrAbgesagt):
			renderStatus(w, r, http.StatusConflict, "meldung.html", meldung{
				Titel: "Nicht mehr möglich", Text: "Der Dienst ist schon voll, der Termin hat begonnen oder wurde abgesagt.",
				LinkZiel: zurueck, LinkText: "Zurück zum Termin",
			})
		case err != nil:
			interner(w, "dienst eintragen", err)
		default:
			if z := r.PostFormValue("zurueck"); z != "" {
				zurueck = sicheresZiel(z)
			}
			umleiten(w, r, zurueck)
		}
	}
}
