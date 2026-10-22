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

	"github.com/ruben-sch/teamtafel/internal/termin"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

// Termine verwaltet Termine und Serien.
type Termine interface {
	Anlegen(ctx context.Context, vereinID, mannschaftID, vonKontoID string, d termin.Daten) (termin.Termin, error)
	Aendern(ctx context.Context, vereinID, terminID, vonKontoID string, d termin.Daten) error
	Absagen(ctx context.Context, vereinID, terminID, vonKontoID string) error
	Termin(ctx context.Context, vereinID, terminID string) (termin.Termin, error)
	Kommende(ctx context.Context, vereinID, mannschaftID string, ab, bis time.Time) ([]termin.Termin, error)
	FuerKonto(ctx context.Context, vereinID, kontoID string, ab, bis time.Time) ([]termin.Termin, error)
	GehoertZumTeam(ctx context.Context, vereinID, mannschaftID, kontoID string) (bool, error)
	SerieAnlegen(ctx context.Context, vereinID, mannschaftID string, d termin.SerieDaten) (termin.Serie, error)
	Serie(ctx context.Context, vereinID, serieID string) (termin.Serie, error)
	Serien(ctx context.Context, vereinID, mannschaftID string) ([]termin.Serie, error)
	SerieBeenden(ctx context.Context, vereinID, serieID string) error
	Rueckmelden(ctx context.Context, vereinID, terminID, spielerID, kontoID, status, grund string, alsTrainer bool) error
	Rueckmeldungen(ctx context.Context, vereinID, terminID string) ([]termin.Rueckmeldung, error)
	MeineSpieler(ctx context.Context, vereinID, kontoID string, terminIDs []string) (map[string][]termin.Rueckmeldung, error)
	Anwesenheit(ctx context.Context, vereinID, mannschaftID string) ([]termin.Anwesenheit, error)
	AnwesenheitSetzen(ctx context.Context, vereinID, terminID, spielerID, kontoID string, anwesend bool) error
	DienstAnlegen(ctx context.Context, vereinID, terminID, bezeichnung string, plaetze int) (termin.Dienst, error)
	DienstAendern(ctx context.Context, vereinID, terminID, dienstID, bezeichnung string, plaetze int) error
	DienstLoeschen(ctx context.Context, vereinID, terminID, dienstID string) error
	Eintragen(ctx context.Context, vereinID, terminID, dienstID, kontoID, vonKontoID string) error
	Austragen(ctx context.Context, vereinID, terminID, dienstID, kontoID, vonKontoID string) error
	Dienste(ctx context.Context, vereinID, terminID string) ([]termin.Dienst, error)
	DienstPersonen(ctx context.Context, vereinID, terminID string) ([]termin.Person, error)
	DienstUebersicht(ctx context.Context, vereinID, kontoID string, terminIDs []string) (map[string]termin.DienstInfo, error)
}

// terminKarte ist ein Termin in einer Liste, mit den eigenen Spielern für Zu und Ab.
type terminKarte struct {
	termin.Termin
	Meine []termin.Rueckmeldung
	// Moeglich: Eltern und Spieler können noch antworten (nicht abgesagt, Frist offen).
	Moeglich bool
	// Zaehler: Zu, Ab und Offen anzeigen (Trainer-Übersicht).
	Zaehler bool
	// Dienste fasst die Dienste zusammen; nil, wenn der Termin keine hat.
	Dienste *termin.DienstInfo
}

// fristOffen: Bis zur Frist, ohne Frist bis zum Beginn, dürfen Eltern und Spieler antworten.
func fristOffen(t termin.Termin) bool {
	ende := t.Beginn
	if t.Frist != nil {
		ende = *t.Frist
	}
	return !t.Abgesagt && time.Now().Before(ende)
}

// karten ergänzt die Termine um die Spieler, für die das Konto antworten darf.
func karten(ctx context.Context, ts Termine, vereinID, kontoID string, termine []termin.Termin, zaehler bool) ([]terminKarte, error) {
	ids := make([]string, len(termine))
	for i, t := range termine {
		ids[i] = t.ID
	}
	meine, err := ts.MeineSpieler(ctx, vereinID, kontoID, ids)
	if err != nil {
		return nil, err
	}
	dienste, err := ts.DienstUebersicht(ctx, vereinID, kontoID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]terminKarte, len(termine))
	for i, t := range termine {
		out[i] = terminKarte{Termin: t, Meine: meine[t.ID], Moeglich: fristOffen(t), Zaehler: zaehler}
		if d, ok := dienste[t.ID]; ok {
			out[i].Dienste = &d
		}
	}
	return out, nil
}

var monateKurz = [...]string{"JAN", "FEB", "MÄR", "APR", "MAI", "JUN", "JUL", "AUG", "SEP", "OKT", "NOV", "DEZ"}

var funcs = template.FuncMap{
	// datum: "Di, 13.10.2026"
	"datum": func(t time.Time) string {
		t = t.In(termin.Zeitzone)
		return termin.Wochentage[t.Weekday()][:2] + ", " + t.Format("02.01.2006")
	},
	"uhrzeit":   func(t time.Time) string { return t.In(termin.Zeitzone).Format("15:04") },
	"typName":   termin.TypName,
	"minuten":   func(d time.Duration) int { return int(d.Minutes()) },
	"statusText": func(s string) string {
		switch s {
		case termin.Zu:
			return "zugesagt"
		case termin.Ab:
			return "abgesagt"
		}
		return "offen"
	},
	"grundText": func(g string) string {
		switch g {
		case termin.GrundKrank:
			return "krank"
		case termin.GrundUrlaub:
			return "Urlaub"
		case termin.GrundSonstiges:
			return "sonstiges"
		}
		return ""
	},
	// Datumsblock der Terminkarte: "DI", "13", "OKT".
	"wtKurz": func(t time.Time) string {
		return strings.ToUpper(termin.Wochentage[t.In(termin.Zeitzone).Weekday()][:2])
	},
	"tagZahl": func(t time.Time) int { return t.In(termin.Zeitzone).Day() },
	"monatKurz": func(t time.Time) string {
		return monateKurz[t.In(termin.Zeitzone).Month()-1]
	},
	// anteil: Prozent von teil an der Summe, für den Zähler-Balken.
	"anteil": func(teil int, alle ...int) int {
		summe := 0
		for _, n := range alle {
			summe += n
		}
		if summe == 0 {
			return 0
		}
		return teil * 100 / summe
	},
	"list": func(s ...string) []string { return s },
	"join": strings.Join,
	// plaetzeFrei: "1 Platz frei", "3 Plätze frei", "alle Plätze belegt".
	"plaetzeFrei": func(n int) string {
		switch n {
		case 0:
			return "alle Plätze belegt"
		case 1:
			return "1 Platz frei"
		}
		return strconv.Itoa(n) + " Plätze frei"
	},
	"inc": func(i int) int { return i + 1 },
}

// heute liefert Mitternacht in Berlin; Listen zeigen auch heutige, schon begonnene Termine.
func heute() time.Time {
	n := time.Now().In(termin.Zeitzone)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, termin.Zeitzone)
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
	Wochentag, Rhythmus, Monatswoche, Uhrzeit, Dauer, Treff, Frist, Ort, GueltigVon, GueltigBis string
}

// neueSerieForm: Vorgaben für das leere Formular.
func neueSerieForm() serieForm {
	return serieForm{Wochentag: "1", Rhythmus: termin.RhythmusWoechentlich, Monatswoche: "1", Dauer: "90",
		GueltigVon: heute().Format("2006-01-02")}
}

func serieFormLesen(r *http.Request) serieForm {
	v := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	return serieForm{Wochentag: v("wochentag"), Rhythmus: v("rhythmus"), Monatswoche: v("monatswoche"), Uhrzeit: v("uhrzeit"), Dauer: v("dauer"), Treff: v("treff"),
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
	d := termin.SerieDaten{Wochentag: time.Weekday(wt % 7), Rhythmus: f.Rhythmus, Uhrzeit: f.Uhrzeit, Dauer: time.Duration(dauer) * time.Minute,
		TreffVorher: time.Duration(treff) * time.Minute, FristVorher: time.Duration(frist) * time.Hour, Ort: f.Ort, GueltigVon: von}
	if f.Rhythmus == termin.RhythmusMonatlich {
		var err error
		if d.Monatswoche, err = strconv.Atoi(f.Monatswoche); err != nil {
			return d, fmt.Errorf("%w: monatswoche", termin.ErrUngueltig)
		}
	}
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

func neuSeite(w http.ResponseWriter, r *http.Request, m verein.Mannschaft, status int, seite terminNeuSeite) {
	seite.Titel, seite.Mannschaft = "Termin anlegen", m
	renderStatus(w, r, status, "termin_neu.html", seite)
}

func (s *teamSeiten) terminNeu(w http.ResponseWriter, r *http.Request, z Zugang[verein.Mannschaft]) {
	neuSeite(w, r, z.Objekt, http.StatusOK, terminNeuSeite{
		Termin: terminForm{Typ: termin.TypTraining},
		Serie:  neueSerieForm(),
	})
}

func (s *teamSeiten) terminAnlegen(w http.ResponseWriter, r *http.Request, z Zugang[verein.Mannschaft]) {
	f := terminFormLesen(r)
	d, err := f.daten()
	var t termin.Termin
	if err == nil {
		t, err = s.termine.Anlegen(r.Context(), z.Verein.ID, z.Objekt.ID, z.Konto.ID, d)
	}
	if errors.Is(err, termin.ErrUngueltig) {
		neuSeite(w, r, z.Objekt, http.StatusBadRequest, terminNeuSeite{Termin: f, Serie: neueSerieForm(), Fehler: terminFehler})
		return
	}
	if err != nil {
		interner(w, "termin anlegen", err)
		return
	}
	umleiten(w, r, "/t/"+t.ID)
}

const terminFehler = "Bitte Datum und Uhrzeiten prüfen: Das Ende muss nach dem Beginn liegen, Treffzeit und Frist davor."

func (s *teamSeiten) serieAnlegen(w http.ResponseWriter, r *http.Request, z Zugang[verein.Mannschaft]) {
	f := serieFormLesen(r)
	d, err := f.daten()
	if err == nil {
		_, err = s.termine.SerieAnlegen(r.Context(), z.Verein.ID, z.Objekt.ID, d)
	}
	if errors.Is(err, termin.ErrUngueltig) {
		neuSeite(w, r, z.Objekt, http.StatusBadRequest, terminNeuSeite{Termin: terminForm{Typ: termin.TypTraining}, Serie: f,
			Fehler: "Bitte Wochentag, Rhythmus, Uhrzeit (HH:MM), Dauer und Startdatum der Serie prüfen."})
		return
	}
	if err != nil {
		interner(w, "serie anlegen", err)
		return
	}
	umleiten(w, r, "/m/"+z.Objekt.ID)
}

func (s *teamSeiten) serieBeenden(w http.ResponseWriter, r *http.Request, z Zugang[termin.Serie]) {
	if err := s.termine.SerieBeenden(r.Context(), z.Verein.ID, z.Objekt.ID); err != nil && !errors.Is(err, termin.ErrNotFound) {
		interner(w, "serie beenden", err)
		return
	}
	umleiten(w, r, "/m/"+z.MannschaftID)
}

type terminSeite struct {
	Titel       string
	Termin      termin.Termin
	Verwalter   bool
	Form        terminForm
	Fehler      string
	Meine       []termin.Rueckmeldung
	Alle        []termin.Rueckmeldung
	FristOffen  bool
	FristVorbei bool
	// Begonnen: Trainer erfassen ab jetzt die Anwesenheit statt Zu und Ab.
	Begonnen bool
	Dienste  []dienstAnsicht
	// ImTeam: Das Konto kann selbst Dienste übernehmen (Vereinsadmins ohne Mannschaft nicht).
	ImTeam       bool
	DienstFehler string
}

func (s *teamSeiten) terminDetail(w http.ResponseWriter, r *http.Request, z Zugang[termin.Termin]) {
	s.terminSeiteZeigen(w, r, z, http.StatusOK, terminSeite{Verwalter: z.Verwalter, Form: terminFormAus(z.Objekt.Daten)})
}

// terminSeiteZeigen rendert den Termin aus dem Zugang; seite bringt Formular und Fehler mit.
func (s *teamSeiten) terminSeiteZeigen(w http.ResponseWriter, r *http.Request, z Zugang[termin.Termin], status int, seite terminSeite) {
	ctx := r.Context()
	v, k, t := z.Verein, z.Konto, z.Objekt
	seite.Termin = t
	meine, err := s.termine.MeineSpieler(ctx, v.ID, k.ID, []string{t.ID})
	if err != nil {
		interner(w, "eigene spieler laden", err)
		return
	}
	if seite.Alle, err = s.termine.Rueckmeldungen(ctx, v.ID, t.ID); err != nil {
		interner(w, "rückmeldungen laden", err)
		return
	}
	if err := s.dienstAnsichten(ctx, v.ID, k.ID, &seite); err != nil {
		interner(w, "dienste laden", err)
		return
	}
	seite.Titel, seite.Meine = t.Mannschaft, meine[t.ID]
	seite.FristOffen = fristOffen(t)
	seite.FristVorbei = !t.Abgesagt && !seite.FristOffen
	seite.Begonnen = !time.Now().Before(t.Beginn)
	renderStatus(w, r, status, "termin.html", seite)
}

func (s *teamSeiten) rueckmelden(w http.ResponseWriter, r *http.Request, z Zugang[termin.Termin]) {
	spieler := r.PathValue("spieler")
	if !uuidPattern.MatchString(spieler) {
		http.NotFound(w, r)
		return
	}
	status, grund := r.PostFormValue("status"), ""
	if status == termin.Ab {
		// Das Formular schickt die Grundauswahl bei beiden Knöpfen mit; nur Absagen haben einen Grund.
		grund = r.PostFormValue("grund")
	}
	err := s.termine.Rueckmelden(r.Context(), z.Verein.ID, z.Objekt.ID, spieler, z.Konto.ID, status, grund, z.Verwalter)
	zurueck := "/t/" + z.Objekt.ID
	switch {
	case errors.Is(err, termin.ErrUngueltig):
		http.Error(w, "Ungültige Rückmeldung", http.StatusBadRequest)
	case errors.Is(err, termin.ErrNichtBerechtigt):
		renderStatus(w, r, http.StatusForbidden, "meldung.html", meldung{
			Titel: "Kein Zugriff", Text: "Für diesen Spieler kannst du nicht zu- oder absagen.", LinkZiel: zurueck, LinkText: "Zurück zum Termin",
		})
	case errors.Is(err, termin.ErrFristVorbei), errors.Is(err, termin.ErrAbgesagt):
		renderStatus(w, r, http.StatusConflict, "meldung.html", meldung{
			Titel: "Nicht mehr möglich", Text: "Die Frist ist vorbei oder der Termin wurde abgesagt. Bitte sag dem Trainer direkt Bescheid.",
			LinkZiel: zurueck, LinkText: "Zurück zum Termin",
		})
	case err != nil:
		interner(w, "rückmelden", err)
	default:
		if z := r.PostFormValue("zurueck"); z != "" {
			zurueck = sicheresZiel(z)
		}
		umleiten(w, r, zurueck)
	}
}

func (s *teamSeiten) terminAendern(w http.ResponseWriter, r *http.Request, z Zugang[termin.Termin]) {
	f := terminFormLesen(r)
	d, err := f.daten()
	if err == nil {
		err = s.termine.Aendern(r.Context(), z.Verein.ID, z.Objekt.ID, z.Konto.ID, d)
	}
	if errors.Is(err, termin.ErrUngueltig) {
		s.terminSeiteZeigen(w, r, z, http.StatusBadRequest, terminSeite{Verwalter: true, Form: f, Fehler: terminFehler})
		return
	}
	if err != nil {
		interner(w, "termin ändern", err)
		return
	}
	umleiten(w, r, "/t/"+z.Objekt.ID)
}

func (s *teamSeiten) terminAbsagen(w http.ResponseWriter, r *http.Request, z Zugang[termin.Termin]) {
	if err := s.termine.Absagen(r.Context(), z.Verein.ID, z.Objekt.ID, z.Konto.ID); err != nil {
		interner(w, "termin absagen", err)
		return
	}
	umleiten(w, r, "/t/"+z.Objekt.ID)
}

// dienstAnsichten lädt die Dienste des Termins und für Trainer, wen sie einteilen können.
func (s *teamSeiten) dienstAnsichten(ctx context.Context, vereinID, kontoID string, seite *terminSeite) error {
	t := seite.Termin
	ds, err := s.termine.Dienste(ctx, vereinID, t.ID)
	if err != nil {
		return err
	}
	seite.ImTeam = !seite.Verwalter
	var personen []termin.Person
	if seite.Verwalter {
		if seite.ImTeam, err = s.termine.GehoertZumTeam(ctx, vereinID, t.MannschaftID, kontoID); err != nil {
			return err
		}
		if personen, err = s.termine.DienstPersonen(ctx, vereinID, t.ID); err != nil {
			return err
		}
	}
	seite.Dienste = make([]dienstAnsicht, len(ds))
	for i, d := range ds {
		a := dienstAnsicht{Dienst: d, Meins: d.Hat(kontoID)}
		for _, p := range personen {
			if !d.Hat(p.KontoID) {
				a.Einteilbar = append(a.Einteilbar, p)
			}
		}
		seite.Dienste[i] = a
	}
	return nil
}
