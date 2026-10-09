package config

import "testing"

func TestBetreiberStandardUndUeberschreiben(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("BETREIBER_NAME", "")
	t.Setenv("BETREIBER_ANSCHRIFT", "")
	t.Setenv("BETREIBER_EMAIL", "")
	c, err := FromEnv("test")
	if err != nil {
		t.Fatal(err)
	}
	if c.BetreiberName != "Ruben Schwarz" || c.BetreiberAnschrift != "Alemannenstraße 15, 75391 Gechingen" ||
		c.BetreiberEmail != "info@schwarzpost.de" {
		t.Errorf("standard = %q, %q, %q", c.BetreiberName, c.BetreiberAnschrift, c.BetreiberEmail)
	}

	t.Setenv("BETREIBER_NAME", "Erika Muster")
	if c, _ := FromEnv("test"); c.BetreiberName != "Erika Muster" {
		t.Errorf("überschrieben = %q", c.BetreiberName)
	}
}
