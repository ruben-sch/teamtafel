package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

// rollen beantwortet, wer verwalten darf: Super-Admins überall, Vereinsadmins
// in ihrem Verein.
type rollen struct {
	vereine     Vereine
	superadmins map[string]bool
}

func (rl *rollen) istSuperadmin(k auth.Konto) bool {
	return rl.superadmins[auth.NormalisiereEmail(k.Email)]
}

// darfVerwalten gilt für die Plattform (ohne Verein) oder den Verein der Anfrage.
func (rl *rollen) darfVerwalten(ctx context.Context, k auth.Konto) (bool, error) {
	if rl.istSuperadmin(k) {
		return true, nil
	}
	v, ok := vereinAus(ctx)
	if !ok {
		return false, nil
	}
	return rl.vereine.IstAdmin(ctx, v.ID, k.ID)
}

type verwaltung struct {
	vereine Vereine
	team    Team
	auth    Auth
	rollen  *rollen
	scheme  string
}

// berechtigt prüft Login und Adminrolle und antwortet sonst selbst.
func (a *verwaltung) berechtigt(w http.ResponseWriter, r *http.Request) (auth.Konto, bool) {
	k, ok := kontoAus(r.Context())
	if !ok {
		ziel := r.URL.Path
		if r.Method != http.MethodGet {
			ziel = "/admin"
		}
		http.Redirect(w, r, "/login?weiter="+url.QueryEscape(ziel), http.StatusSeeOther)
		return k, false
	}
	ok, err := a.rollen.darfVerwalten(r.Context(), k)
	if err != nil {
		interner(w, "adminrolle prüfen", err)
		return k, false
	}
	if !ok {
		renderStatus(w, http.StatusForbidden, "meldung.html", meldung{
			Titel: "Kein Zugriff", Text: "Diese Seite ist nur für Admins.", LinkZiel: "/", LinkText: "Zur Startseite",
		})
	}
	return k, ok
}

func (a *verwaltung) seite(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.berechtigt(w, r); !ok {
		return
	}
	a.zeigen(w, r, http.StatusOK, "")
}

// zeigen rendert die Plattform- oder die Vereinsverwaltung, je nach Host.
func (a *verwaltung) zeigen(w http.ResponseWriter, r *http.Request, status int, fehler string) {
	if v, ok := vereinAus(r.Context()); ok {
		a.vereinSeite(w, r, v, status, fehler)
		return
	}
	vs, err := a.vereine.Alle(r.Context())
	if err != nil {
		interner(w, "vereine laden", err)
		return
	}
	type eintrag struct {
		verein.Verein
		URL string
	}
	data := struct {
		Titel   string
		Vereine []eintrag
		Fehler  string
		Form    map[string]string
	}{Titel: "Plattform-Verwaltung", Fehler: fehler, Form: formWerte(r, "slug", "name", "admin")}
	for _, v := range vs {
		data.Vereine = append(data.Vereine, eintrag{v, a.scheme + "://" + v.Slug + "." + r.Host})
	}
	renderStatus(w, status, "admin_plattform.html", data)
}

type adminMannschaft struct {
	verein.Mannschaft
	Trainer []team.Trainer
}

func (a *verwaltung) vereinSeite(w http.ResponseWriter, r *http.Request, v verein.Verein, status int, fehler string) {
	ctx := r.Context()
	ms, err := a.vereine.Mannschaften(ctx, v.ID)
	if err != nil {
		interner(w, "mannschaften laden", err)
		return
	}
	data := struct {
		Titel        string
		Verein       verein.Verein
		Mannschaften []adminMannschaft
		Admins       []string
		Saison       string
		Fehler       string
		Form         map[string]string
	}{Titel: "Verwaltung " + v.Name, Verein: v, Saison: aktuelleSaison(time.Now()), Fehler: fehler,
		Form: formWerte(r, "saison", "name")}
	if data.Form["saison"] != "" {
		data.Saison = data.Form["saison"]
	}
	for _, m := range ms {
		tr, err := a.team.Trainer(ctx, v.ID, m.ID)
		if err != nil {
			interner(w, "trainer laden", err)
			return
		}
		data.Mannschaften = append(data.Mannschaften, adminMannschaft{m, tr})
	}
	if data.Admins, err = a.vereine.Admins(ctx, v.ID); err != nil {
		interner(w, "admins laden", err)
		return
	}
	renderStatus(w, status, "admin_verein.html", data)
}

func (a *verwaltung) vereinAnlegen(w http.ResponseWriter, r *http.Request) {
	k, ok := a.berechtigt(w, r)
	if !ok {
		return
	}
	if _, imVerein := vereinAus(r.Context()); imVerein || !a.rollen.istSuperadmin(k) {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	adminEmail := r.PostFormValue("admin")
	if _, err := a.auth.KontoFuer(ctx, adminEmail); errors.Is(err, auth.ErrUngueltigeAdresse) {
		a.zeigen(w, r, http.StatusBadRequest, "Die Admin-Adresse sieht nicht nach einer E-Mail-Adresse aus.")
		return
	}
	v, err := a.vereine.Anlegen(ctx, strings.ToLower(strings.TrimSpace(r.PostFormValue("slug"))), r.PostFormValue("name"))
	switch {
	case errors.Is(err, verein.ErrSlugVergeben):
		a.zeigen(w, r, http.StatusBadRequest, "Diese Subdomain ist schon vergeben.")
		return
	case errors.Is(err, verein.ErrUngueltigerSlug):
		a.zeigen(w, r, http.StatusBadRequest,
			"Die Subdomain darf nur Kleinbuchstaben, Ziffern und Bindestriche enthalten und nicht reserviert sein.")
		return
	case errors.Is(err, verein.ErrUngueltigerName):
		a.zeigen(w, r, http.StatusBadRequest, "Bitte einen Namen angeben.")
		return
	case err != nil:
		interner(w, "verein anlegen", err)
		return
	}
	if err := a.adminEintragen(ctx, v.ID, adminEmail); err != nil {
		interner(w, "vereinsadmin eintragen", err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *verwaltung) adminEintragen(ctx context.Context, vereinID, email string) error {
	k, err := a.auth.KontoFuer(ctx, email)
	if err != nil {
		return err
	}
	return a.vereine.AdminHinzufuegen(ctx, vereinID, k.ID)
}

// imVerein liefert den Verein für Vereinsaktionen und antwortet auf der Hauptdomain mit 404.
func (a *verwaltung) imVerein(w http.ResponseWriter, r *http.Request) (verein.Verein, bool) {
	if _, ok := a.berechtigt(w, r); !ok {
		return verein.Verein{}, false
	}
	v, ok := vereinAus(r.Context())
	if !ok {
		http.NotFound(w, r)
	}
	return v, ok
}

func (a *verwaltung) adminHinzufuegen(w http.ResponseWriter, r *http.Request) {
	v, ok := a.imVerein(w, r)
	if !ok {
		return
	}
	err := a.adminEintragen(r.Context(), v.ID, r.PostFormValue("email"))
	if errors.Is(err, auth.ErrUngueltigeAdresse) {
		a.zeigen(w, r, http.StatusBadRequest, "Das sieht nicht nach einer E-Mail-Adresse aus.")
		return
	}
	if err != nil {
		interner(w, "vereinsadmin eintragen", err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *verwaltung) farbeSetzen(w http.ResponseWriter, r *http.Request) {
	v, ok := a.imVerein(w, r)
	if !ok {
		return
	}
	err := a.vereine.FarbeSetzen(r.Context(), v.ID, r.PostFormValue("farbe"))
	if errors.Is(err, verein.ErrUngueltigeFarbe) {
		a.zeigen(w, r, http.StatusBadRequest, "Die Farbe ist zu hell. Weiße Schrift muss darauf gut lesbar bleiben.")
		return
	}
	if err != nil {
		interner(w, "vereinsfarbe setzen", err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *verwaltung) mannschaftAnlegen(w http.ResponseWriter, r *http.Request) {
	v, ok := a.imVerein(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		a.zeigen(w, r, http.StatusBadRequest, "Bitte einen Namen für die Mannschaft angeben.")
		return
	}
	_, err := a.vereine.MannschaftAnlegen(r.Context(), v.ID, strings.TrimSpace(r.PostFormValue("saison")), name)
	if errors.Is(err, verein.ErrUngueltigeSaison) {
		a.zeigen(w, r, http.StatusBadRequest, "Die Saison muss im Format JJJJ/JJ sein, z. B. "+aktuelleSaison(time.Now())+".")
		return
	}
	if errors.Is(err, verein.ErrMannschaftVorhanden) {
		a.zeigen(w, r, http.StatusBadRequest, "Diese Mannschaft gibt es in der Saison schon.")
		return
	}
	if err != nil {
		interner(w, "mannschaft anlegen", err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// mannschaftImVerein prüft, ob die Mannschaft aus dem Pfad zum Verein gehört.
func (a *verwaltung) mannschaftImVerein(w http.ResponseWriter, r *http.Request) (verein.Verein, string, bool) {
	v, ok := a.imVerein(w, r)
	if !ok {
		return v, "", false
	}
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		http.NotFound(w, r)
		return v, "", false
	}
	if _, err := a.team.Mannschaft(r.Context(), v.ID, id); errors.Is(err, verein.ErrNotFound) {
		http.NotFound(w, r)
		return v, "", false
	} else if err != nil {
		interner(w, "mannschaft laden", err)
		return v, "", false
	}
	return v, id, true
}

func (a *verwaltung) trainerHinzufuegen(w http.ResponseWriter, r *http.Request) {
	v, id, ok := a.mannschaftImVerein(w, r)
	if !ok {
		return
	}
	k, err := a.auth.KontoFuer(r.Context(), r.PostFormValue("email"))
	if errors.Is(err, auth.ErrUngueltigeAdresse) {
		a.zeigen(w, r, http.StatusBadRequest, "Die Trainer-Adresse sieht nicht nach einer E-Mail-Adresse aus.")
		return
	}
	if err == nil {
		err = a.team.TrainerHinzufuegen(r.Context(), v.ID, id, k.ID)
	}
	if err != nil {
		interner(w, "trainer eintragen", err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *verwaltung) trainerEntfernen(w http.ResponseWriter, r *http.Request) {
	v, id, ok := a.mannschaftImVerein(w, r)
	if !ok {
		return
	}
	konto := r.PathValue("konto")
	if !uuidPattern.MatchString(konto) {
		http.NotFound(w, r)
		return
	}
	if err := a.team.TrainerEntfernen(r.Context(), v.ID, id, konto); err != nil {
		interner(w, "trainer entfernen", err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// aktuelleSaison liefert die Saison zum Datum; sie beginnt am 1. Juli.
func aktuelleSaison(t time.Time) string {
	y := t.Year()
	if t.Month() < time.July {
		y--
	}
	return fmt.Sprintf("%d/%02d", y, (y+1)%100)
}

// formWerte übernimmt Eingaben eines POST, damit sie nach einem Fehler stehen bleiben.
func formWerte(r *http.Request, felder ...string) map[string]string {
	out := map[string]string{}
	if r.Method != http.MethodPost {
		return out
	}
	for _, f := range felder {
		out[f] = r.PostFormValue(f)
	}
	return out
}
