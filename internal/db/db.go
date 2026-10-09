// Package db kapselt Verbindungsaufbau und Migrationen.
package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registriert den Treiber "pgx" für goose
	"github.com/pressly/goose/v3"

	"github.com/ruben-sch/teamtafel/migrations"
)

// Connect öffnet einen Verbindungspool und prüft die Verbindung.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("pool anlegen: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("datenbank nicht erreichbar: %w", err)
	}
	return pool, nil
}

// Migrate spielt alle eingebetteten Migrationen ein.
func Migrate(ctx context.Context, url string) error {
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("verbindung für migrationen: %w", err)
	}
	defer sqlDB.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		return fmt.Errorf("migrationen laden: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrationen ausführen: %w", err)
	}
	return nil
}
