package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/termin"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

// Das Zugriffsmodul entscheidet, ob ein Konto eine Route für das Objekt aus dem Pfad
// nutzen darf. Jede Route wird mit ihrer Regel registriert; der Handler läuft nur,
// wenn die Regel erfüllt ist, und bekommt das Ergebnis als Zugang. Fachliche Rechte
// (für welchen Spieler jemand antworten darf, wer Dienste übernimmt) prüft termin.

// Zugang ist, was das Zugriffsmodul für eine Anfrage aufgelöst hat.
type Zugang[O any] struct {
	Verein verein.Verein
	// ImVerein ist false nur bei Routen, die auch auf der Hauptdomain laufen.
	ImVerein bool
	Konto    auth.Konto
	// Objekt ist das Objekt aus {id}, Keins bei Routen ohne Objekt.
	Objekt       O
	MannschaftID string
	// Trainer: Das Konto trainiert die Mannschaft des Objekts.
	Trainer bool
	// Verwalter: Trainer der Mannschaft oder Admin (Plattform oder Verein).
	Verwalter bool
}

// Keins ist das Objekt von Routen ohne {id}.
type Keins struct{}

// Handler bekommt den Zugang statt ihn selbst zu ermitteln.
type Handler[O any] func(w http.ResponseWriter, r *http.Request, z Zugang[O])

type stufe int

const (
	stufeKonto        stufe = iota + 1 // angemeldet, Verein optional
	stufeKontoAPI                      // wie stufeKonto, ohne Login 401 statt Weiterleitung
	stufeAngemeldet                    // angemeldet im Verein
	stufeTeam                          // Verwalter oder im Team der Mannschaft
	stufeVerwalter                     // Trainer der Mannschaft oder Admin
	stufeTrainer                       // nur Trainer der Mannschaft
	stufeAdmin                         // Admin des Vereins oder Plattform, Verein optional
	stufeVereinsadmin                  // Admin des Vereins der Anfrage
	stufeSuperadmin                    // Plattform-Admin, nur auf der Hauptdomain
)

// Regel sagt, wer eine Route nutzen darf; Text ersetzt den Hinweis bei 403.
type Regel struct {
	stufe stufe
	text  string
}

// Text liefert die Regel mit eigenem Hinweis für die 403-Seite.
func (g Regel) Text(s string) Regel {
	g.text = s
	return g
}

var (
	Konto        = Regel{stufe: stufeKonto}
	KontoAPI     = Regel{stufe: stufeKontoAPI}
	Angemeldet   = Regel{stufe: stufeAngemeldet}
	TeamMitglied = Regel{stufe: stufeTeam, text: "Diese Seite sieht nur das Team."}
	Verwalter    = Regel{stufe: stufeVerwalter, text: "Diese Seite sehen nur die Trainer der Mannschaft."}
	Trainer      = Regel{stufe: stufeTrainer, text: "Diese Seite sehen nur die Trainer der Mannschaft."}
	Admin        = Regel{stufe: stufeAdmin, text: "Diese Seite ist nur für Admins."}
	Vereinsadmin = Regel{stufe: stufeVereinsadmin, text: "Diese Seite ist nur für Admins."}
	Superadmin   = Regel{stufe: stufeSuperadmin, text: "Diese Seite ist nur für Admins."}
)

func (g Regel) mitObjekt() bool {
	return g.stufe == stufeTeam || g.stufe == stufeVerwalter || g.stufe == stufeTrainer || g.stufe == stufeVereinsadmin
}

func (g Regel) ohneObjekt() bool {
	return g.stufe != stufeTeam && g.stufe != stufeVerwalter && g.stufe != stufeTrainer
}

// errUnbekannt: Das Objekt gibt es im Verein der Anfrage nicht (oder RLS verbirgt es).
var errUnbekannt = errors.New("objekt unbekannt")

// Objekt sagt, wie das Zugriffsmodul {id} lädt.
type Objekt[O any] struct {
	art string
	// seite ist die Seite des Objekts, die das Team sieht; nil, wenn es keine gibt.
	seite     func(id string) string
	seiteText string
	// laden liefert das Objekt und seine Mannschaft oder errUnbekannt.
	laden func(ctx context.Context, vereinID, id string) (O, string, error)
}

// beitrittsanfrage ist eine Beitrittsanfrage, von der Handler nur die ID brauchen.
type beitrittsanfrage struct{ ID string }

// zugriff kennt die Stores, aus denen es Objekte und Rollen ermittelt.
type zugriff struct {
	rollen  *rollen
	team    Team
	termine Termine
}

func (z *zugriff) mannschaft() Objekt[verein.Mannschaft] {
	return Objekt[verein.Mannschaft]{art: "mannschaft", laden: func(ctx context.Context, vereinID, id string) (verein.Mannschaft, string, error) {
		m, err := z.team.Mannschaft(ctx, vereinID, id)
		if errors.Is(err, verein.ErrNotFound) {
			err = errUnbekannt
		}
		return m, m.ID, err
	}}
}

func (z *zugriff) termin() Objekt[termin.Termin] {
	return Objekt[termin.Termin]{art: "termin", seite: func(id string) string { return "/t/" + id }, seiteText: "Zurück zum Termin",
		laden: func(ctx context.Context, vereinID, id string) (termin.Termin, string, error) {
			t, err := z.termine.Termin(ctx, vereinID, id)
			if errors.Is(err, termin.ErrNotFound) {
				err = errUnbekannt
			}
			return t, t.MannschaftID, err
		}}
}

func (z *zugriff) serie() Objekt[termin.Serie] {
	return Objekt[termin.Serie]{art: "serie", laden: func(ctx context.Context, vereinID, id string) (termin.Serie, string, error) {
		s, err := z.termine.Serie(ctx, vereinID, id)
		if errors.Is(err, termin.ErrNotFound) {
			err = errUnbekannt
		}
		return s, s.MannschaftID, err
	}}
}

func (z *zugriff) anfrage() Objekt[beitrittsanfrage] {
	return Objekt[beitrittsanfrage]{art: "anfrage", laden: func(ctx context.Context, vereinID, id string) (beitrittsanfrage, string, error) {
		m, err := z.team.AnfrageMannschaft(ctx, vereinID, id)
		if errors.Is(err, team.ErrAnfrageUnbekannt) {
			err = errUnbekannt
		}
		return beitrittsanfrage{ID: id}, m, err
	}}
}

// route ist eine registrierte Route mit ihrer Regel; der Routentest prüft sie alle.
type route struct {
	muster string
	regel  Regel
	// art ist das Objekt aus {id}, leer ohne Objekt.
	art string
	// grund steht nur bei öffentlichen Routen.
	grund string
}

// router nimmt Routen nur mit Regel an; der Mux bleibt privat.
type router struct {
	mux    *http.ServeMux
	z      *zugriff
	routen []route
}

func neuerRouter(z *zugriff) *router {
	return &router{mux: http.NewServeMux(), z: z}
}

func (rt *router) ServeHTTP(w http.ResponseWriter, r *http.Request) { rt.mux.ServeHTTP(w, r) }

// oeffentlich registriert eine Route ohne Login; grund sagt, warum das in Ordnung ist.
func (rt *router) oeffentlich(muster, grund string, h http.HandlerFunc) {
	if grund == "" {
		panic("öffentliche Route ohne Grund: " + muster)
	}
	rt.routen = append(rt.routen, route{muster: muster, grund: grund})
	rt.mux.HandleFunc(muster, h)
}

// ohneObjekt registriert eine Route, deren Regel kein Objekt braucht.
func (rt *router) ohneObjekt(muster string, g Regel, h Handler[Keins]) {
	if !g.ohneObjekt() {
		panic("Regel braucht ein Objekt: " + muster)
	}
	rt.routen = append(rt.routen, route{muster: muster, regel: g})
	rt.mux.HandleFunc(muster, func(w http.ResponseWriter, r *http.Request) {
		zg, ok := rt.z.ohneObjekt(w, r, g)
		if ok {
			h(w, r, zg)
		}
	})
}

// mitObjekt registriert eine Route, deren {id} ein Objekt ist. Eine Funktion statt
// Methode, weil Methoden in Go keine Typparameter haben.
func mitObjekt[O any](rt *router, muster string, g Regel, o Objekt[O], h Handler[O]) {
	if !strings.Contains(muster, "{id}") {
		panic("Route mit Objekt ohne {id}: " + muster)
	}
	if !g.mitObjekt() {
		panic("Regel ohne Objekt: " + muster)
	}
	rt.routen = append(rt.routen, route{muster: muster, regel: g, art: o.art})
	rt.mux.HandleFunc(muster, func(w http.ResponseWriter, r *http.Request) {
		zg, ok := pruefeObjekt(rt.z, w, r, g, o)
		if ok {
			h(w, r, zg)
		}
	})
}

// ohneObjekt prüft Login, Verein und Adminrollen; sonst antwortet es selbst.
func (z *zugriff) ohneObjekt(w http.ResponseWriter, r *http.Request, g Regel) (Zugang[Keins], bool) {
	ctx := r.Context()
	var zg Zugang[Keins]
	zg.Verein, zg.ImVerein = vereinAus(ctx)
	if !zg.ImVerein && (g.stufe == stufeAngemeldet || g.stufe == stufeVereinsadmin) {
		http.NotFound(w, r)
		return zg, false
	}
	var ok bool
	if zg.Konto, ok = kontoAus(ctx); !ok {
		zumLogin(w, r, g)
		return zg, false
	}
	switch g.stufe {
	case stufeSuperadmin:
		if zg.ImVerein {
			http.NotFound(w, r)
			return zg, false
		}
		zg.Verwalter = z.rollen.istSuperadmin(zg.Konto)
	case stufeAdmin, stufeVereinsadmin:
		admin, err := z.rollen.darfVerwalten(ctx, zg.Konto)
		if err != nil {
			interner(w, "adminrolle prüfen", err)
			return zg, false
		}
		zg.Verwalter = admin
	default:
		return zg, true
	}
	if !zg.Verwalter {
		keinZugriff(w, r, g.text, "/", "Zur Startseite")
	}
	return zg, zg.Verwalter
}

// pruefeObjekt lädt das Objekt aus {id} im Verein und prüft die Rolle an seiner Mannschaft.
func pruefeObjekt[O any](z *zugriff, w http.ResponseWriter, r *http.Request, g Regel, o Objekt[O]) (Zugang[O], bool) {
	ctx := r.Context()
	var zg Zugang[O]
	if zg.Verein, zg.ImVerein = vereinAus(ctx); !zg.ImVerein {
		http.NotFound(w, r)
		return zg, false
	}
	var ok bool
	if zg.Konto, ok = kontoAus(ctx); !ok {
		zumLogin(w, r, g)
		return zg, false
	}
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		http.NotFound(w, r)
		return zg, false
	}
	var err error
	zg.Objekt, zg.MannschaftID, err = o.laden(ctx, zg.Verein.ID, id)
	if errors.Is(err, errUnbekannt) {
		http.NotFound(w, r)
		return zg, false
	}
	if err != nil {
		interner(w, "zugriff "+o.art, err)
		return zg, false
	}
	rl, err := z.rolle(ctx, zg.Verein.ID, zg.Konto, zg.MannschaftID, g.stufe)
	if err != nil {
		interner(w, "berechtigung prüfen", err)
		return zg, false
	}
	zg.Trainer, zg.Verwalter = rl.trainer, rl.verwalter
	erlaubt := rl.erfuellt(g.stufe)
	switch {
	case erlaubt:
	case rl.team && o.seite != nil:
		keinZugriff(w, r, g.text, o.seite(id), o.seiteText)
	default:
		keinZugriff(w, r, g.text, "/", "Zur Startseite")
	}
	return zg, erlaubt
}

// rolle ist, was ein Konto an einer Mannschaft ist.
type rolle struct {
	trainer, verwalter, team bool
}

func (rl rolle) erfuellt(s stufe) bool {
	switch s {
	case stufeTeam:
		return rl.team
	case stufeTrainer:
		return rl.trainer
	case stufeVerwalter, stufeVereinsadmin:
		return rl.verwalter
	}
	return false
}

// rolle fragt nur ab, was die Stufe braucht: Trainer, dann Admin, dann Team.
// Auf Admin-Routen zählt nur die Adminrolle, Trainer verwalten dort nichts.
func (z *zugriff) rolle(ctx context.Context, vereinID string, k auth.Konto, mannschaftID string, s stufe) (rolle, error) {
	var rl rolle
	var err error
	if s != stufeVereinsadmin {
		if rl.trainer, err = z.team.IstTrainer(ctx, vereinID, mannschaftID, k.ID); err != nil {
			return rl, err
		}
	}
	rl.verwalter = rl.trainer
	if !rl.trainer && s != stufeTrainer {
		if rl.verwalter, err = z.rollen.darfVerwalten(ctx, k); err != nil {
			return rl, err
		}
	}
	rl.team = rl.verwalter
	if !rl.team && z.termine != nil && (s == stufeTeam || s == stufeVerwalter) {
		if rl.team, err = z.termine.GehoertZumTeam(ctx, vereinID, mannschaftID, k.ID); err != nil {
			return rl, err
		}
	}
	return rl, nil
}

// zumLogin schickt zur Anmeldung und danach zurück: bei GET auf die Seite, sonst auf
// die Seite, von der das Formular kam.
func zumLogin(w http.ResponseWriter, r *http.Request, g Regel) {
	if g.stufe == stufeKontoAPI {
		http.Error(w, "Nicht angemeldet", http.StatusUnauthorized)
		return
	}
	ziel := r.URL.Path
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		ziel = herkunft(r)
	}
	umleiten(w, r, "/login?weiter="+url.QueryEscape(ziel))
}

// herkunft ist der Pfad des Referers im Verein der Anfrage, sonst "/".
func herkunft(r *http.Request) string {
	u, err := url.Parse(r.Referer())
	if err != nil || u.Host != r.Host {
		return "/"
	}
	pfad, ok := strings.CutPrefix(u.Path, basis(r))
	if !ok || !strings.HasPrefix(pfad, "/") {
		return "/"
	}
	return sicheresZiel(pfad)
}

func keinZugriff(w http.ResponseWriter, r *http.Request, text, ziel, linkText string) {
	renderStatus(w, r, http.StatusForbidden, "meldung.html", meldung{
		Titel: "Kein Zugriff", Text: text, LinkZiel: ziel, LinkText: linkText,
	})
}
