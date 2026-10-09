// Package config liest die Konfiguration aus Umgebungsvariablen.
package config

import (
	"errors"
	"os"
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
	// Version wird beim Build gesetzt und auf der Startseite angezeigt.
	Version string
}

// FromEnv baut die Konfiguration aus der Umgebung.
func FromEnv(version string) (Config, error) {
	c := Config{
		ListenAddr:         getenv("LISTEN_ADDR", ":8080"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		MigrateDatabaseURL: os.Getenv("MIGRATE_DATABASE_URL"),
		Version:            version,
	}
	if c.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL ist nicht gesetzt")
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
