package web

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

var kennungMuster = regexp.MustCompile(`(?s)<div class="kennung".*?</div>\s*</div>`)

// kennungIn liefert den Block „Angemeldet als …“ der Seite, sonst "".
func kennungIn(body string) string {
	return kennungMuster.FindString(body)
}

func TestKopfzeileZeigtKontoUndRolle(t *testing.T) {
	w := neueTerminWelt(t)
	tm := w.termin(t, nil)
	ctx := context.Background()
	trainerCookie, trainer := w.anmelden(fmt.Sprintf("chef-%d@example.org", time.Now().UnixNano()))
	if err := w.team.TrainerHinzufuegen(ctx, w.verein.ID, w.bambini.ID, trainer.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.vereine.AdminHinzufuegen(ctx, w.verein.ID, trainer.ID); err != nil {
		t.Fatal(err)
	}

	faelle := []struct {
		name   string
		c      *http.Cookie
		pfad   string
		wollen []string
	}{
		{"eltern start", w.eltern, "/", []string{">Eltern von Ben<"}},
		{"eltern termin", w.eltern, "/t/" + tm.ID, []string{">Eltern von Ben<"}},
		{"eltern einstellungen", w.eltern, "/einstellungen", []string{">Eltern von Ben<"}},
		{"trainer termin", trainerCookie, "/t/" + tm.ID, []string{trainer.Email, ">Trainer<", ">Admin<"}},
		{"trainer mannschaft", trainerCookie, "/m/" + w.bambini.ID, []string{trainer.Email, ">Trainer<"}},
		{"ohne rolle", w.fremd, "/", []string{">Noch ohne Team<"}},
	}
	for _, f := range faelle {
		rec := w.do(http.MethodGet, f.pfad, nil, f.c)
		k := kennungIn(rec.Body.String())
		if rec.Code != http.StatusOK || !strings.Contains(k, "Angemeldet als") {
			t.Errorf("%s: %d ohne kennung: %s", f.name, rec.Code, rec.Body.String())
			continue
		}
		for _, s := range f.wollen {
			if !strings.Contains(k, s) {
				t.Errorf("%s: %q fehlt in %s", f.name, s, k)
			}
		}
	}
	if strings.Contains(kennungIn(w.do(http.MethodGet, "/", nil, w.eltern).Body.String()), ">Trainer<") {
		t.Error("eltern als trainer angezeigt")
	}

	// Ohne Anmeldung keine Kennung.
	if k := kennungIn(w.do(http.MethodGet, "/login", nil, nil).Body.String()); k != "" {
		t.Errorf("login zeigt kennung: %s", k)
	}
}
