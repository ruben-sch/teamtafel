package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ruben-sch/teamtafel/internal/verein"
)

func TestImpressumUndDatenschutzOhneLogin(t *testing.T) {
	h := NewHandler(Options{
		DB:       fakePinger{},
		BaseHost: "teamtafel.example",
		Vereine:  fakeVereine{vereine: map[string]verein.Verein{"fc": {ID: "id-fc", Slug: "fc", Name: "FC Beispiel"}}},
		Betreiber: Betreiber{Name: "Erika Muster", Anschrift: "Hauptstr. 1, 75391 Gechingen",
			Email: "kontakt@example.org"},
	})
	for _, host := range []string{"teamtafel.example", "fc.teamtafel.example"} {
		rec := get(h, host, "/impressum")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s/impressum: %d", host, rec.Code)
		}
		for _, want := range []string{"Erika Muster", "Hauptstr. 1, 75391 Gechingen", "kontakt@example.org"} {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("%s/impressum ohne %q", host, want)
			}
		}
		rec = get(h, host, "/datenschutz")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s/datenschutz: %d", host, rec.Code)
		}
		for _, want := range []string{"Erika Muster", "Hetzner", "Resend", "90 Tage", "Aufsichtsbehörde"} {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("%s/datenschutz ohne %q", host, want)
			}
		}
	}
}

func TestFusszeileVerlinktRechtliches(t *testing.T) {
	body := get(testHandler(fakePinger{}), "teamtafel.example", "/login").Body.String()
	for _, want := range []string{`href="/impressum"`, `href="/datenschutz"`} {
		if !strings.Contains(body, want) {
			t.Errorf("login-seite ohne %s", want)
		}
	}
}

func TestImpressumOhneAngabenWeistDaraufHin(t *testing.T) {
	body := get(testHandler(fakePinger{}), "teamtafel.example", "/impressum").Body.String()
	if !strings.Contains(body, "noch nicht hinterlegt") {
		t.Errorf("fehlender hinweis: %s", body)
	}
}
