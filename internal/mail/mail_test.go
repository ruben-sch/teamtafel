package mail_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ruben-sch/teamtafel/internal/mail"
)

// Läuft gegen Mailpit: TEST_SMTP_ADDR (z. B. localhost:1025) und
// TEST_MAILPIT_URL (z. B. http://localhost:8025). In CI Pflicht.
func mailpit(t *testing.T) (string, string) {
	t.Helper()
	smtpAddr, api := os.Getenv("TEST_SMTP_ADDR"), os.Getenv("TEST_MAILPIT_URL")
	if smtpAddr == "" || api == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_SMTP_ADDR und TEST_MAILPIT_URL müssen in CI gesetzt sein")
		}
		t.Skip("Mailpit nicht konfiguriert")
	}
	return smtpAddr, api
}

func TestSendenKommtBeiMailpitAn(t *testing.T) {
	smtpAddr, api := mailpit(t)
	host, port, _ := strings.Cut(smtpAddr, ":")
	s := mail.NewSMTP(mail.Config{Host: host, Port: port, From: "Teamtafel <noreply@teamtafel.example>"})

	an := "test-" + time.Now().Format("150405.000000") + "@example.org"
	if err := s.Senden(context.Background(), an, "Dein Anmeldelink für Teamtafel", "Hallo,\nhier ist dein Link: https://x/auth/abc\n"); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(api + "/api/v1/search?query=to:" + an)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var res struct {
		Messages []struct {
			ID      string
			Subject string
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Subject != "Dein Anmeldelink für Teamtafel" {
		t.Fatalf("mailpit: %+v", res.Messages)
	}

	resp2, err := http.Get(api + "/api/v1/message/" + res.Messages[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	var msg struct {
		Text string
		From struct{ Address string }
	}
	if err := json.NewDecoder(resp2.Body).Decode(&msg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.Text, "https://x/auth/abc") || msg.From.Address != "noreply@teamtafel.example" {
		t.Fatalf("nachricht: %+v", msg)
	}
}

func TestSendenLehntZeilenumbruchImEmpfaengerAb(t *testing.T) {
	s := mail.NewSMTP(mail.Config{Host: "127.0.0.1", Port: "1", From: "a@b.de"})
	if err := s.Senden(context.Background(), "a@b.de\r\nBcc: x@y.de", "x", "y"); err == nil {
		t.Fatal("header-injection muss abgelehnt werden")
	}
}
