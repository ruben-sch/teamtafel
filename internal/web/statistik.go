package web

import (
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
