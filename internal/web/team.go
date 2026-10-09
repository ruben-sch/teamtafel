package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"rsc.io/qr"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

// Team verwaltet Trainer, Kader, Team-Links und Beitrittsanfragen.
type Team interface {
	IstTrainer(ctx context.Context, vereinID, mannschaftID, kontoID string) (bool, error)
	TrainerMannschaften(ctx context.Context, vereinID, kontoID string) ([]verein.Mannschaft, error)
	Mannschaft(ctx context.Context, vereinID, mannschaftID string) (verein.Mannschaft, error)
	EinladungErneuern(ctx context.Context, vereinID, mannschaftID string) (string, error)
	Einladung(ctx context.Context, vereinID, token string) (verein.Mannschaft, error)
	AnfrageStellen(ctx context.Context, vereinID, mannschaftID, kontoID string, d team.AnfrageDaten) (team.Anfrage, error)
	OffeneAnfragen(ctx context.Context, vereinID, mannschaftID string) ([]team.Anfrage, error)
	AnfrageMannschaft(ctx context.Context, vereinID, anfrageID string) (string, error)
	Freigeben(ctx context.Context, vereinID, anfrageID, trainerKontoID string, gleicherSpieler bool) error
	Ablehnen(ctx context.Context, vereinID, anfrageID string) error
	Kader(ctx context.Context, vereinID, mannschaftID string) ([]team.Spieler, error)
	MeineSpieler(ctx context.Context, vereinID, kontoID string) ([]team.Spieler, error)
}

type teamSeiten struct {
	team   Team
	scheme string
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// angemeldet liefert Verein und Konto. Ohne Verein antwortet es mit 404,
// ohne Login leitet es zur Anmeldung und danach zurück.
func angemeldet(w http.ResponseWriter, r *http.Request) (verein.Verein, auth.Konto, bool) {
	v, ok := vereinAus(r.Context())
	if !ok {
		http.NotFound(w, r)
		return v, auth.Konto{}, false
	}
	k, ok := kontoAus(r.Context())
	if !ok {
		http.Redirect(w, r, "/login?weiter="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
		return v, k, false
	}
	return v, k, true
}

func interner(w http.ResponseWriter, was string, err error) {
	slog.Error(was, "err", err)
	http.Error(w, "Interner Fehler", http.StatusInternalServerError)
}

// trainerVon prüft, ob das Konto die Mannschaft trainiert, und antwortet sonst selbst.
func (s *teamSeiten) trainerVon(w http.ResponseWriter, r *http.Request, mannschaftID string) (verein.Verein, auth.Konto, bool) {
	v, k, ok := angemeldet(w, r)
	if !ok {
		return v, k, false
	}
	if !uuidPattern.MatchString(mannschaftID) {
		http.NotFound(w, r)
		return v, k, false
	}
	ist, err := s.team.IstTrainer(r.Context(), v.ID, mannschaftID, k.ID)
	if err != nil {
		interner(w, "trainer prüfen", err)
		return v, k, false
	}
	if !ist {
		renderStatus(w, http.StatusForbidden, "meldung.html", meldung{
			Titel: "Kein Zugriff", Text: "Diese Seite sehen nur die Trainer der Mannschaft.", LinkZiel: "/", LinkText: "Zur Startseite",
		})
		return v, k, false
	}
	return v, k, true
}

type mannschaftSeite struct {
	Titel      string
	Mannschaft verein.Mannschaft
	Anfragen   []team.Anfrage
	Kader      []team.Spieler
}

func (s *teamSeiten) mannschaft(w http.ResponseWriter, r *http.Request) {
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
	seite := mannschaftSeite{Titel: m.Name, Mannschaft: m}
	if seite.Anfragen, err = s.team.OffeneAnfragen(ctx, v.ID, id); err != nil {
		interner(w, "anfragen laden", err)
		return
	}
	if seite.Kader, err = s.team.Kader(ctx, v.ID, id); err != nil {
		interner(w, "kader laden", err)
		return
	}
	render(w, "mannschaft.html", seite)
}

func (s *teamSeiten) einladung(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v, _, ok := s.trainerVon(w, r, id)
	if !ok {
		return
	}
	m, err := s.team.Mannschaft(r.Context(), v.ID, id)
	if err != nil {
		interner(w, "mannschaft laden", err)
		return
	}
	token, err := s.team.EinladungErneuern(r.Context(), v.ID, id)
	if err != nil {
		interner(w, "team-link erneuern", err)
		return
	}
	link := s.scheme + "://" + r.Host + "/join/" + token
	svg, err := qrSVG(link)
	if err != nil {
		interner(w, "qr-code", err)
		return
	}
	render(w, "einladung.html", struct {
		Titel      string
		Mannschaft verein.Mannschaft
		Link       string
		QR         template.HTML
	}{"Team-Link " + m.Name, m, link, svg})
}

type joinSeite struct {
	Titel      string
	Mannschaft verein.Mannschaft
	Daten      team.AnfrageDaten
	Fehler     string
}

func (s *teamSeiten) joinMannschaft(w http.ResponseWriter, r *http.Request) (verein.Verein, auth.Konto, verein.Mannschaft, bool) {
	v, k, ok := angemeldet(w, r)
	if !ok {
		return v, k, verein.Mannschaft{}, false
	}
	m, err := s.team.Einladung(r.Context(), v.ID, r.PathValue("token"))
	if errors.Is(err, team.ErrEinladungUngueltig) {
		renderStatus(w, http.StatusNotFound, "meldung.html", meldung{
			Titel: "Link ungültig", Text: "Der Team-Link ist nicht mehr gültig. Frag beim Trainer nach dem aktuellen Link.",
			LinkZiel: "/", LinkText: "Zur Startseite",
		})
		return v, k, m, false
	}
	if err != nil {
		interner(w, "team-link prüfen", err)
		return v, k, m, false
	}
	return v, k, m, true
}

func (s *teamSeiten) joinFormular(w http.ResponseWriter, r *http.Request) {
	_, _, m, ok := s.joinMannschaft(w, r)
	if !ok {
		return
	}
	render(w, "join.html", joinSeite{Titel: m.Name, Mannschaft: m, Daten: team.AnfrageDaten{Art: team.ArtKind}})
}

func (s *teamSeiten) joinAnfragen(w http.ResponseWriter, r *http.Request) {
	v, k, m, ok := s.joinMannschaft(w, r)
	if !ok {
		return
	}
	jahrgang, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue("jahrgang")))
	d := team.AnfrageDaten{
		Art: r.PostFormValue("art"), Vorname: r.PostFormValue("vorname"),
		Nachname: r.PostFormValue("nachname"), Jahrgang: jahrgang,
	}
	a, err := s.team.AnfrageStellen(r.Context(), v.ID, m.ID, k.ID, d)
	if errors.Is(err, team.ErrUngueltigeAngaben) {
		renderStatus(w, http.StatusBadRequest, "join.html", joinSeite{
			Titel: m.Name, Mannschaft: m, Daten: d, Fehler: "Bitte Vorname, Nachname und Jahrgang angeben.",
		})
		return
	}
	if err != nil {
		interner(w, "anfrage stellen", err)
		return
	}
	render(w, "join_gesendet.html", struct {
		Titel      string
		Mannschaft verein.Mannschaft
		Anfrage    team.Anfrage
	}{m.Name, m, a})
}

// anfrageEntscheiden prüft, ob das Konto die Mannschaft der Anfrage trainiert,
// führt die Entscheidung aus und leitet zurück zur Mannschaft.
func (s *teamSeiten) anfrageEntscheiden(entscheiden func(ctx context.Context, vereinID, anfrageID string, k auth.Konto, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, _, ok := angemeldet(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if !uuidPattern.MatchString(id) {
			http.NotFound(w, r)
			return
		}
		mannschaftID, err := s.team.AnfrageMannschaft(r.Context(), v.ID, id)
		if errors.Is(err, team.ErrAnfrageUnbekannt) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			interner(w, "anfrage laden", err)
			return
		}
		_, k, ok := s.trainerVon(w, r, mannschaftID)
		if !ok {
			return
		}
		err = entscheiden(r.Context(), v.ID, id, k, r)
		if errors.Is(err, team.ErrAnfrageUnbekannt) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			interner(w, "anfrage entscheiden", err)
			return
		}
		http.Redirect(w, r, "/m/"+mannschaftID, http.StatusSeeOther)
	}
}

func (s *teamSeiten) freigeben(ctx context.Context, vereinID, anfrageID string, k auth.Konto, r *http.Request) error {
	return s.team.Freigeben(ctx, vereinID, anfrageID, k.ID, r.PostFormValue("gleich") != "")
}

func (s *teamSeiten) ablehnen(ctx context.Context, vereinID, anfrageID string, _ auth.Konto, _ *http.Request) error {
	return s.team.Ablehnen(ctx, vereinID, anfrageID)
}

// qrSVG zeichnet den Link als QR-Code mit vier Modulen Ruhezone.
func qrSVG(link string) (template.HTML, error) {
	c, err := qr.Encode(link, qr.M)
	if err != nil {
		return "", err
	}
	const rand = 4
	var p strings.Builder
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Black(x, y) {
				fmt.Fprintf(&p, "M%d %dh1v1h-1z", x+rand, y+rand)
			}
		}
	}
	n := c.Size + 2*rand
	// Der Pfad enthält nur Zahlen und Buchstaben aus diesem Code, daher kein Escaping nötig.
	return template.HTML(fmt.Sprintf( //nolint:gosec // siehe Kommentar
		`<svg class="qr" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" role="img" aria-label="QR-Code des Team-Links">`+
			`<rect width="%d" height="%d" fill="#fff"/><path d="%s" fill="#000"/></svg>`, n, n, n, n, p.String())), nil
}
