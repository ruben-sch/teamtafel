package web

import (
	"errors"
	"net/http"

	"github.com/ruben-sch/teamtafel/internal/termin"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

type statistikSeite struct {
	Titel      string
	Mannschaft verein.Mannschaft
	Spieler    []termin.Anwesenheit
}

// statistik zeigt den Trainern die Beteiligung je Spieler an den bisherigen Terminen der Saison.
// Nur Trainer, nicht Vereinsadmins: Die Zahlen sind für die Arbeit mit der Mannschaft gedacht.
func (s *teamSeiten) statistik(w http.ResponseWriter, r *http.Request, z Zugang[verein.Mannschaft]) {
	m := z.Objekt
	seite := statistikSeite{Titel: "Anwesenheit " + m.Name, Mannschaft: m}
	var err error
	if seite.Spieler, err = s.termine.Anwesenheit(r.Context(), z.Verein.ID, m.ID); err != nil {
		interner(w, "anwesenheit laden", err)
		return
	}
	render(w, r, "statistik.html", seite)
}

// anwesenheitSetzen: Trainer halten nach Beginn fest, wer da war. Die Rückmeldung bleibt, niemand wird benachrichtigt.
func (s *teamSeiten) anwesenheitSetzen(w http.ResponseWriter, r *http.Request, z Zugang[termin.Termin]) {
	zurueck := "/t/" + z.Objekt.ID
	spieler := r.PathValue("spieler")
	if !uuidPattern.MatchString(spieler) {
		http.NotFound(w, r)
		return
	}
	err := s.termine.AnwesenheitSetzen(r.Context(), z.Verein.ID, z.Objekt.ID, spieler, z.Konto.ID, r.PostFormValue("anwesend") == "1")
	switch {
	case errors.Is(err, termin.ErrNichtBerechtigt):
		http.NotFound(w, r)
	case errors.Is(err, termin.ErrNichtBegonnen), errors.Is(err, termin.ErrAbgesagt):
		renderStatus(w, r, http.StatusConflict, "meldung.html", meldung{
			Titel: "Nicht möglich", Text: "Die Anwesenheit lässt sich erst ab Beginn und nicht für abgesagte Termine eintragen.",
			LinkZiel: zurueck, LinkText: "Zurück zum Termin",
		})
	case err != nil:
		interner(w, "anwesenheit setzen", err)
	default:
		umleiten(w, r, zurueck)
	}
}
