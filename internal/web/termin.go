package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/termin"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

// Termine verwaltet Termine und Serien.
type Termine interface {
	Anlegen(ctx context.Context, vereinID, mannschaftID string, d termin.Daten) (termin.Termin, error)
	Aendern(ctx context.Context, vereinID, terminID string, d termin.Daten) error
	Absagen(ctx context.Context, vereinID, terminID string) error
	Termin(ctx context.Context, vereinID, terminID string) (termin.Termin, error)
	Kommende(ctx context.Context, vereinID, mannschaftID string, ab, bis time.Time) ([]termin.Termin, error)
	FuerKonto(ctx context.Context, vereinID, kontoID string, ab, bis time.Time) ([]termin.Termin, error)
	GehoertZumTeam(ctx context.Context, vereinID, mannschaftID, kontoID string) (bool, error)
	SerieAnlegen(ctx context.Context, vereinID, mannschaftID string, d termin.SerieDaten) (termin.Serie, error)
	Serie(ctx context.Context, vereinID, serieID string) (termin.Serie, error)
	Serien(ctx context.Context, vereinID, mannschaftID string) ([]termin.Serie, error)
	SerieBeenden(ctx context.Context, vereinID, serieID string) error
}

var wochentage = [...]string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"}

var funcs = template.FuncMap{
	// datum: "Di, 13.10.2026"
	"datum": func(t time.Time) string {
		t = t.In(termin.Zeitzone)
		return wochentage[t.Weekday()][:2] + ", " + t.Format("02.01.2006")
	},
	"uhrzeit":   func(t time.Time) string { return t.In(termin.Zeitzone).Format("15:04") },
	"wochentag": func(w time.Weekday) string { return wochentage[w] },
	"typName": func(typ string) string {
		switch typ {
		case termin.TypSpiel:
			return "Spiel"
		case termin.TypSonstiges:
			return "Termin"
		}
		return "Training"
	},
	"minuten": func(d time.Duration) int { return int(d.Minutes()) },
	"list":    func(s ...string) []string { return s },
	"inc":     func(i int) int { return i + 1 },
}

// heute liefert Mitternacht in Berlin; Listen zeigen auch heutige, schon begonnene Termine.
func heute() time.Time {
	n := time.Now().In(termin.Zeitzone)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, termin.Zeitzone)
}

// verwalterVon lässt Trainer der Mannschaft und Admins durch und antwortet sonst selbst.
func (s *teamSeiten) verwalterVon(w http.ResponseWriter, r *http.Request, mannschaftID string) (verein.Verein, auth.Konto, bool) {
	v, k, ok := angemeldet(w, r)
	if !ok {
		return v, k, false
	}
	if !uuidPattern.MatchString(mannschaftID) {
		http.NotFound(w, r)
		return v, k, false
	}
	ok, err := s.istVerwalter(r.Context(), v, k, mannschaftID)
	if err != nil {
		interner(w, "berechtigung prüfen", err)
		return v, k, false
	}
	if !ok {
		renderStatus(w, http.StatusForbidden, "meldung.html", meldung{
			Titel: "Kein Zugriff", Text: "Diese Seite sehen nur die Trainer der Mannschaft.", LinkZiel: "/", LinkText: "Zur Startseite",
		})
	}
	return v, k, ok
}

func (s *teamSeiten) istVerwalter(ctx context.Context, v verein.Verein, k auth.Konto, mannschaftID string) (bool, error) {
	ok, err := s.team.IstTrainer(ctx, v.ID, mannschaftID, k.ID)
	if err != nil || ok {
		return ok, err
	}
	return s.rollen.darfVerwalten(ctx, k)
}

// terminForm sind die Formularwerte, als Text, damit Eingaben nach Fehlern stehen bleiben.
type terminForm struct {
	Typ, Titel, Datum, Beginn, Ende, Treffzeit, Frist, Ort, Treffpunkt string
}

func terminFormAus(d termin.Daten) terminForm {
	f := terminForm{Typ: d.Typ, Titel: d.Titel, Ort: d.Ort, Treffpunkt: d.Treffpunkt}
	b := d.Beginn.In(termin.Zeitzone)
	f.Datum, f.Beginn, f.Ende = b.Format("2006-01-02"), b.Format("15:04"), d.Ende.In(termin.Zeitzone).Format("15:04")
	if d.Treffzeit != nil {
		f.Treffzeit = d.Treffzeit.In(termin.Zeitzone).Format("15:04")
	}
	if d.Frist != nil {
		f.Frist = d.Frist.In(termin.Zeitzone).Format("2006-01-02T15:04")
	}
	return f
}

func terminFormLesen(r *http.Request) terminForm {
	v := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	return terminForm{Typ: v("typ"), Titel: v("titel"), Datum: v("datum"), Beginn: v("beginn"), Ende: v("ende"),
		Treffzeit: v("treffzeit"), Frist: v("frist"), Ort: v("ort"), Treffpunkt: v("treffpunkt")}
}

func (f terminForm) daten() (termin.Daten, error) {
	d := termin.Daten{Typ: f.Typ, Titel: f.Titel, Ort: f.Ort, Treffpunkt: f.Treffpunkt}
	am := func(uhr string) (time.Time, error) {
		return time.ParseInLocation("2006-01-02 15:04", f.Datum+" "+uhr, termin.Zeitzone)
	}
	var err error
	if d.Beginn, err = am(f.Beginn); err != nil {
		return d, fmt.Errorf("%w: beginn", termin.ErrUngueltig)
	}
	if d.Ende, err = am(f.Ende); err != nil {
		return d, fmt.Errorf("%w: ende", termin.ErrUngueltig)
	}
	if f.Treffzeit != "" {
		t, err := am(f.Treffzeit)
		if err != nil {
			return d, fmt.Errorf("%w: treffzeit", termin.ErrUngueltig)
		}
		d.Treffzeit = &t
	}
	if f.Frist != "" {
		t, err := time.ParseInLocation("2006-01-02T15:04", f.Frist, termin.Zeitzone)
		if err != nil {
			return d, fmt.Errorf("%w: frist", termin.ErrUngueltig)
		}
		d.Frist = &t
	}
	return d, nil
}

type serieForm struct {
	Wochentag, Uhrzeit, Dauer, Treff, Frist, Ort, GueltigVon, GueltigBis string
}

func serieFormLesen(r *http.Request) serieForm {
	v := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	return serieForm{Wochentag: v("wochentag"), Uhrzeit: v("uhrzeit"), Dauer: v("dauer"), Treff: v("treff"),
		Frist: v("frist"), Ort: v("ort"), GueltigVon: v("gueltig_von"), GueltigBis: v("gueltig_bis")}
}

func (f serieForm) daten() (termin.SerieDaten, error) {
	zahl := func(s string) (int, error) {
		if s == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return 0, termin.ErrUngueltig
		}
		return n, nil
	}
	wt, err1 := strconv.Atoi(f.Wochentag)
	dauer, err2 := zahl(f.Dauer)
	treff, err3 := zahl(f.Treff)
	frist, err4 := zahl(f.Frist)
	von, err5 := time.Parse("2006-01-02", f.GueltigVon)
	if err := errors.Join(err1, err2, err3, err4, err5); err != nil || wt < 1 || wt > 7 {
		return termin.SerieDaten{}, fmt.Errorf("%w: %v", termin.ErrUngueltig, err)
	}
	d := termin.SerieDaten{Wochentag: time.Weekday(wt % 7), Uhrzeit: f.Uhrzeit, Dauer: time.Duration(dauer) * time.Minute,
		TreffVorher: time.Duration(treff) * time.Minute, FristVorher: time.Duration(frist) * time.Hour, Ort: f.Ort, GueltigVon: von}
	if f.GueltigBis != "" {
		bis, err := time.Parse("2006-01-02", f.GueltigBis)
		if err != nil {
			return d, fmt.Errorf("%w: gültig bis", termin.ErrUngueltig)
		}
		d.GueltigBis = &bis
	}
	return d, nil
}

type terminNeuSeite struct {
	Titel      string
	Mannschaft verein.Mannschaft
	Termin     terminForm
	Serie      serieForm
	Fehler     string
}

func (s *teamSeiten) neuSeite(w http.ResponseWriter, r *http.Request, v verein.Verein, status int, seite terminNeuSeite) {
	m, err := s.team.Mannschaft(r.Context(), v.ID, r.PathValue("id"))
	if err != nil {
		interner(w, "mannschaft laden", err)
		return
	}
	seite.Titel, seite.Mannschaft = "Termin anlegen", m
	renderStatus(w, status, "termin_neu.html", seite)
}

func (s *teamSeiten) terminNeu(w http.ResponseWriter, r *http.Request) {
	v, _, ok := s.verwalterVon(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	s.neuSeite(w, r, v, http.StatusOK, terminNeuSeite{
		Termin: terminForm{Typ: termin.TypTraining},
		Serie:  serieForm{Wochentag: "1", Dauer: "90", GueltigVon: heute().Format("2006-01-02")},
	})
}

func (s *teamSeiten) terminAnlegen(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v, _, ok := s.verwalterVon(w, r, id)
	if !ok {
		return
	}
	f := terminFormLesen(r)
	d, err := f.daten()
	var t termin.Termin
	if err == nil {
		t, err = s.termine.Anlegen(r.Context(), v.ID, id, d)
	}
	if errors.Is(err, termin.ErrUngueltig) {
		s.neuSeite(w, r, v, http.StatusBadRequest, terminNeuSeite{Termin: f, Serie: serieForm{Wochentag: "1", Dauer: "90",
			GueltigVon: heute().Format("2006-01-02")}, Fehler: terminFehler})
		return
	}
	if err != nil {
		interner(w, "termin anlegen", err)
		return
	}
	http.Redirect(w, r, "/t/"+t.ID, http.StatusSeeOther)
}

const terminFehler = "Bitte Datum und Uhrzeiten prüfen: Das Ende muss nach dem Beginn liegen, Treffzeit und Frist davor."

func (s *teamSeiten) serieAnlegen(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v, _, ok := s.verwalterVon(w, r, id)
	if !ok {
		return
	}
	f := serieFormLesen(r)
	d, err := f.daten()
	if err == nil {
		_, err = s.termine.SerieAnlegen(r.Context(), v.ID, id, d)
	}
	if errors.Is(err, termin.ErrUngueltig) {
		s.neuSeite(w, r, v, http.StatusBadRequest, terminNeuSeite{Termin: terminForm{Typ: termin.TypTraining}, Serie: f,
			Fehler: "Bitte Wochentag, Uhrzeit (HH:MM), Dauer und Startdatum der Serie prüfen."})
		return
	}
	if err != nil {
		interner(w, "serie anlegen", err)
		return
	}
	http.Redirect(w, r, "/m/"+id, http.StatusSeeOther)
}

func (s *teamSeiten) serieBeenden(w http.ResponseWriter, r *http.Request) {
	v, _, ok := angemeldet(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	sr, err := s.termine.Serie(r.Context(), v.ID, id)
	if errors.Is(err, termin.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		interner(w, "serie laden", err)
		return
	}
	if _, _, ok := s.verwalterVon(w, r, sr.MannschaftID); !ok {
		return
	}
	if err := s.termine.SerieBeenden(r.Context(), v.ID, id); err != nil && !errors.Is(err, termin.ErrNotFound) {
		interner(w, "serie beenden", err)
		return
	}
	http.Redirect(w, r, "/m/"+sr.MannschaftID, http.StatusSeeOther)
}

// terminLaden liefert den Termin aus dem Pfad und ob das Konto ihn verwalten darf.
// Wer weder zum Team gehört noch verwalten darf, bekommt 403.
func (s *teamSeiten) terminLaden(w http.ResponseWriter, r *http.Request) (verein.Verein, termin.Termin, bool, bool) {
	v, k, ok := angemeldet(w, r)
	if !ok {
		return v, termin.Termin{}, false, false
	}
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		http.NotFound(w, r)
		return v, termin.Termin{}, false, false
	}
	ctx := r.Context()
	t, err := s.termine.Termin(ctx, v.ID, id)
	if errors.Is(err, termin.ErrNotFound) {
		http.NotFound(w, r)
		return v, t, false, false
	}
	if err != nil {
		interner(w, "termin laden", err)
		return v, t, false, false
	}
	verwalter, err := s.istVerwalter(ctx, v, k, t.MannschaftID)
	if err != nil {
		interner(w, "berechtigung prüfen", err)
		return v, t, false, false
	}
	team := verwalter
	if !team {
		if team, err = s.termine.GehoertZumTeam(ctx, v.ID, t.MannschaftID, k.ID); err != nil {
			interner(w, "team prüfen", err)
			return v, t, false, false
		}
	}
	if !team {
		renderStatus(w, http.StatusForbidden, "meldung.html", meldung{
			Titel: "Kein Zugriff", Text: "Diesen Termin sieht nur das Team.", LinkZiel: "/", LinkText: "Zur Startseite",
		})
		return v, t, false, false
	}
	return v, t, verwalter, true
}

type terminSeite struct {
	Titel     string
	Termin    termin.Termin
	Verwalter bool
	Form      terminForm
	Fehler    string
}

func (s *teamSeiten) terminDetail(w http.ResponseWriter, r *http.Request) {
	_, t, verwalter, ok := s.terminLaden(w, r)
	if !ok {
		return
	}
	render(w, "termin.html", terminSeite{Titel: t.Mannschaft, Termin: t, Verwalter: verwalter, Form: terminFormAus(t.Daten)})
}

func (s *teamSeiten) verwalterTermin(w http.ResponseWriter, r *http.Request) (verein.Verein, termin.Termin, bool) {
	v, t, verwalter, ok := s.terminLaden(w, r)
	if ok && !verwalter {
		renderStatus(w, http.StatusForbidden, "meldung.html", meldung{
			Titel: "Kein Zugriff", Text: "Termine ändern nur die Trainer.", LinkZiel: "/t/" + t.ID, LinkText: "Zurück zum Termin",
		})
		return v, t, false
	}
	return v, t, ok
}

func (s *teamSeiten) terminAendern(w http.ResponseWriter, r *http.Request) {
	v, t, ok := s.verwalterTermin(w, r)
	if !ok {
		return
	}
	f := terminFormLesen(r)
	d, err := f.daten()
	if err == nil {
		err = s.termine.Aendern(r.Context(), v.ID, t.ID, d)
	}
	if errors.Is(err, termin.ErrUngueltig) {
		renderStatus(w, http.StatusBadRequest, "termin.html", terminSeite{Titel: t.Mannschaft, Termin: t, Verwalter: true,
			Form: f, Fehler: terminFehler})
		return
	}
	if err != nil {
		interner(w, "termin ändern", err)
		return
	}
	http.Redirect(w, r, "/t/"+t.ID, http.StatusSeeOther)
}

func (s *teamSeiten) terminAbsagen(w http.ResponseWriter, r *http.Request) {
	v, t, ok := s.verwalterTermin(w, r)
	if !ok {
		return
	}
	if err := s.termine.Absagen(r.Context(), v.ID, t.ID); err != nil {
		interner(w, "termin absagen", err)
		return
	}
	http.Redirect(w, r, "/t/"+t.ID, http.StatusSeeOther)
}
