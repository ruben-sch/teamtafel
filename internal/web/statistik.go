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
func (s *teamSeiten) statistik(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v, _, ok := s.trainerVon(w, r, id)
	if !ok {
		return
	}
	ctx := r.Context()
	m, err := s.team.Mannschaft(ctx, v.ID, id)
	if err != nil {
		interner(w, "mannschaft laden", err)
		return
	}
	seite := statistikSeite{Titel: "Anwesenheit " + m.Name, Mannschaft: m}
	if seite.Spieler, err = s.termine.Anwesenheit(ctx, v.ID, id); err != nil {
		interner(w, "anwesenheit laden", err)
		return
	}
	render(w, r, "statistik.html", seite)
}

// anwesenheitSetzen: Trainer halten nach Beginn fest, wer da war. Die Rückmeldung bleibt, niemand wird benachrichtigt.
func (s *teamSeiten) anwesenheitSetzen(w http.ResponseWriter, r *http.Request) {
	v, t, verwalter, ok := s.terminLaden(w, r)
	if !ok {
		return
	}
	zurueck := "/t/" + t.ID
	if !verwalter {
		renderStatus(w, r, http.StatusForbidden, "meldung.html", meldung{
			Titel: "Kein Zugriff", Text: "Die Anwesenheit tragen nur die Trainer ein.", LinkZiel: zurueck, LinkText: "Zurück zum Termin",
		})
		return
	}
	spieler := r.PathValue("spieler")
	if !uuidPattern.MatchString(spieler) {
		http.NotFound(w, r)
		return
	}
	k, _ := kontoAus(r.Context())
	err := s.termine.AnwesenheitSetzen(r.Context(), v.ID, t.ID, spieler, k.ID, r.PostFormValue("anwesend") == "1")
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
