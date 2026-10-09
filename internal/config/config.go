// Package config liest die Konfiguration aus Umgebungsvariablen.
package config

import (
	"errors"
	"net/mail"
	"os"
	"strings"
)

// Config enthält alle Einstellungen der Anwendung.
type Config struct {
	// ListenAddr ist die Adresse des HTTP-Servers, z. B. ":8080".
	ListenAddr string
	// DatabaseURL verbindet mit der App-Rolle (ohne BYPASSRLS).
	DatabaseURL string
	// MigrateDatabaseURL verbindet mit der Owner-Rolle für Migrationen.
	// Leer bedeutet: DatabaseURL wird auch für Migrationen verwendet.
	MigrateDatabaseURL string
	// BaseHost ist die Hauptdomain; Vereine liegen auf <slug>.<BaseHost>.
	BaseHost string
	// Scheme für Links in Mails, "https" oder lokal "http".
	Scheme string
	// SMTP-Zugang für den Mailversand.
	SMTPHost, SMTPPort, SMTPUser, SMTPPassword string
	// MailFrom ist der Absender, z. B. "Teamtafel <teamtafel@schwarzpost.de>".
	MailFrom string
	// Superadmins sind die Plattform-Admins (SUPERADMIN_EMAILS, durch Komma oder Leerzeichen getrennt).
	Superadmins []string
	// MailAllowlist beschränkt Benachrichtigungen auf diese Adressen (MAIL_ALLOWLIST, für Staging); leer heißt alle.
	MailAllowlist []string
	// VAPID-Schlüssel für Web Push (base64url); ohne beide gibt es nur E-Mails.
	VAPIDPublicKey, VAPIDPrivateKey string
	// VAPIDSubject identifiziert den Absender bei den Push-Diensten, Standard mailto:<MAIL_FROM-Adresse>.
	VAPIDSubject string
	// Version wird beim Build gesetzt und auf der Startseite angezeigt.
	Version string
}

// FromEnv baut die Konfiguration aus der Umgebung.
func FromEnv(version string) (Config, error) {
	c := Config{
		ListenAddr:         getenv("LISTEN_ADDR", ":8080"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		MigrateDatabaseURL: os.Getenv("MIGRATE_DATABASE_URL"),
		BaseHost:           strings.ToLower(getenv("APP_HOST", "localhost")),
		Scheme:             getenv("APP_SCHEME", "https"),
		SMTPHost:           getenv("SMTP_HOST", "localhost"),
		SMTPPort:           getenv("SMTP_PORT", "587"),
		SMTPUser:           os.Getenv("SMTP_USER"),
		SMTPPassword:       os.Getenv("SMTP_PASSWORD"),
		MailFrom:           os.Getenv("MAIL_FROM"),
		Version:            version,
		Superadmins:        liste(os.Getenv("SUPERADMIN_EMAILS")),
		MailAllowlist:      liste(os.Getenv("MAIL_ALLOWLIST")),
		VAPIDPublicKey:     os.Getenv("VAPID_PUBLIC_KEY"),
		VAPIDPrivateKey:    os.Getenv("VAPID_PRIVATE_KEY"),
		VAPIDSubject:       os.Getenv("VAPID_SUBJECT"),
	}
	if c.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL ist nicht gesetzt")
	}
	if c.MailFrom == "" {
		c.MailFrom = "Teamtafel <noreply@" + c.BaseHost + ">"
	}
	if c.VAPIDSubject == "" {
		if a, err := mail.ParseAddress(c.MailFrom); err == nil {
			c.VAPIDSubject = "mailto:" + a.Address
		}
	}
	if c.MigrateDatabaseURL == "" {
		c.MigrateDatabaseURL = c.DatabaseURL
	}
	return c, nil
}

// liste trennt Adressen an Komma, Semikolon oder Leerzeichen.
func liste(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' })
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
