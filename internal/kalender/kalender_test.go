package kalender_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/dbtest"
	"github.com/ruben-sch/teamtafel/internal/kalender"
	"github.com/ruben-sch/teamtafel/internal/termin"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

func TestTokenErneuernUndAufloesen(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := context.Background()
	vs := verein.NewStore(pool)
	a, _ := vs.Anlegen(ctx, fmt.Sprintf("ka-%d", time.Now().UnixNano()), "A")
	b, _ := vs.Anlegen(ctx, fmt.Sprintf("kb-%d", time.Now().UnixNano()), "B")
	k, _ := auth.NewStore(pool).KontoFuer(ctx, fmt.Sprintf("kal-%d@example.org", time.Now().UnixNano()))
	s := kalender.NewStore(pool)

	if ok, _ := s.Vorhanden(ctx, a.ID, k.ID); ok {
		t.Fatal("vorher schon vorhanden")
	}
	alt, err := s.Erneuern(ctx, a.ID, k.ID)
	if err != nil || len(alt) < 40 {
		t.Fatalf("token %q, %v", alt, err)
	}
	if got, err := s.Konto(ctx, a.ID, alt); err != nil || got != k.ID {
		t.Fatalf("konto = %q, %v", got, err)
	}
	if ok, _ := s.Vorhanden(ctx, a.ID, k.ID); !ok {
		t.Error("nicht vorhanden")
	}
	// Nur im eigenen Verein gültig.
	if _, err := s.Konto(ctx, b.ID, alt); !errors.Is(err, kalender.ErrUnbekannt) {
		t.Errorf("fremder verein: %v", err)
	}
	neu, _ := s.Erneuern(ctx, a.ID, k.ID)
	if _, err := s.Konto(ctx, a.ID, alt); !errors.Is(err, kalender.ErrUnbekannt) {
		t.Errorf("alter link gilt noch: %v", err)
	}
	if got, _ := s.Konto(ctx, a.ID, neu); got != k.ID {
		t.Error("neuer link ungültig")
	}
}

func TestICS(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	treff := time.Date(2026, 10, 17, 9, 15, 0, 0, berlin)
	ts := []termin.Termin{
		{ID: "t1", Mannschaft: "E1", Daten: termin.Daten{Typ: termin.TypSpiel, Titel: "gegen SV Nachbar; Heim, Liga",
			Beginn: time.Date(2026, 10, 17, 10, 0, 0, 0, berlin), Ende: time.Date(2026, 10, 17, 11, 30, 0, 0, berlin),
			Treffzeit: &treff, Ort: "Sportplatz\nGechingen", Treffpunkt: "Vereinsheim"}},
		{ID: "t2", Mannschaft: "E1", Abgesagt: true, Daten: termin.Daten{Typ: termin.TypTraining,
			Beginn: time.Date(2026, 10, 20, 17, 30, 0, 0, berlin), Ende: time.Date(2026, 10, 20, 19, 0, 0, 0, berlin)}},
	}
	var sb strings.Builder
	kalender.ICS(&sb, kalender.Kopf{Name: "FC Test", Host: "fc.teamtafel.example", Scheme: "https"}, ts,
		time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	out := sb.String()

	for _, want := range []string{
		"BEGIN:VCALENDAR\r\nVERSION:2.0\r\n",
		"X-WR-CALNAME:FC Test\r\n",
		"UID:t1@fc.teamtafel.example\r\n",
		"DTSTAMP:20261009T120000Z\r\n",
		"DTSTART:20261017T080000Z\r\n",
		"DTEND:20261017T093000Z\r\n",
		"SUMMARY:E1: Spiel gegen SV Nachbar\\; Heim\\, Liga\r\n",
		"LOCATION:Sportplatz\\nGechingen\r\n",
		"URL:https://fc.teamtafel.example/t/t1\r\n",
		"SUMMARY:Abgesagt: E1: Training\r\n",
		"STATUS:CANCELLED\r\n",
		"END:VCALENDAR\r\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fehlt %q in\n%s", want, out)
		}
	}
	if !strings.Contains(out, "Treffen 09:15 Uhr\\, Vereinsheim") {
		t.Errorf("treffzeit fehlt:\n%s", out)
	}
	for _, zeile := range strings.Split(out, "\r\n") {
		if len(zeile) > 75 {
			t.Errorf("zeile länger als 75 oktette: %q", zeile)
		}
	}
	if strings.Contains(strings.ReplaceAll(out, "\r\n", ""), "\n") {
		t.Error("nacktes LF")
	}
}
