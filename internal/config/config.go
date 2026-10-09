// Package config liest die Konfiguration aus Umgebungsvariablen.
package config

import (
	"errors"
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
	// MailFrom ist der Absender, z. B. "Teamtafel <noreply@teamtafel.schwarzpost.de>".
	MailFrom string
	// Superadmins sind die Plattform-Admins (SUPERADMIN_EMAILS, durch Komma oder Leerzeichen getrennt).
	Superadmins []string
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
		Superadmins: strings.FieldsFunc(os.Getenv("SUPERADMIN_EMAILS"), func(r rune) bool {
			return r == ',' || r == ' ' || r == ';'
		}),
	}
	if c.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL ist nicht gesetzt")
	}
	if c.MailFrom == "" {
		c.MailFrom = "Teamtafel <noreply@" + c.BaseHost + ">"
	}
	if c.MigrateDatabaseURL == "" {
		c.MigrateDatabaseURL = c.DatabaseURL
	}
	return c, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
