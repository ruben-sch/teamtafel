// Package mail verschickt Text-Mails per SMTP (lokal Mailpit, sonst Resend).
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

// Config beschreibt den SMTP-Zugang.
type Config struct {
	Host     string
	Port     string
	User     string
	Password string
	// From, z. B. "Teamtafel <teamtafel@schwarzpost.de>".
	From string
}

// SMTP verschickt Mails über einen SMTP-Server mit STARTTLS, sofern angeboten.
type SMTP struct {
	cfg Config
}

// NewSMTP erzeugt einen Sender.
func NewSMTP(cfg Config) *SMTP {
	return &SMTP{cfg: cfg}
}

// Senden verschickt eine Text-Mail an genau einen Empfänger.
func (s *SMTP) Senden(ctx context.Context, an, betreff, text string) error {
	from, err := mail.ParseAddress(s.cfg.From)
	if err != nil {
		return fmt.Errorf("absender: %w", err)
	}
	to, err := mail.ParseAddress(an)
	if err != nil || strings.ContainsAny(an, "\r\n") {
		// Ohne Adresse: Fehler landen in Logs und in der Job-Tabelle.
		return errors.New("empfänger ungültig")
	}
	msg, err := nachricht(from, to, betreff, text)
	if err != nil {
		return err
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	d := net.Dialer{Deadline: deadline}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(s.cfg.Host, s.cfg.Port))
	if err != nil {
		return fmt.Errorf("smtp verbinden: %w", err)
	}
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp: %w", err)
	}
	defer func() { _ = c.Close() }()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if s.cfg.User != "" {
		// PlainAuth verweigert den Versand ohne TLS, außer an localhost.
		if err := c.Auth(smtp.PlainAuth("", s.cfg.User, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("smtp-anmeldung: %w", err)
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		// Die Serverantwort enthält oft die Adresse; weitergegeben wird nur der Statuscode.
		var te *textproto.Error
		if errors.As(err, &te) {
			return fmt.Errorf("smtp rcpt to abgelehnt: %d", te.Code)
		}
		return errors.New("smtp rcpt to fehlgeschlagen")
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp schreiben: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp abschließen: %w", err)
	}
	return c.Quit()
}

func nachricht(from, to *mail.Address, betreff, text string) ([]byte, error) {
	if strings.ContainsAny(betreff, "\r\n") {
		return nil, errors.New("betreff enthält zeilenumbruch")
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	domain := from.Address[strings.LastIndex(from.Address, "@")+1:]

	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from.String())
	fmt.Fprintf(&b, "To: %s\r\n", to.String())
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", betreff))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), domain)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(strings.ReplaceAll(text, "\n", "\r\n"))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
