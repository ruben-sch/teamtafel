package mail

import (
	"context"
	"strings"
	"testing"
)

func TestFehlerOhneAdresse(t *testing.T) {
	s := NewSMTP(Config{Host: "127.0.0.1", Port: "1", From: "Teamtafel <noreply@example.org>"})
	err := s.Senden(context.Background(), "geheim@example.org\r\nBcc: x", "Betreff", "Text")
	if err == nil || strings.Contains(err.Error(), "geheim") {
		t.Errorf("err = %v", err)
	}
}
