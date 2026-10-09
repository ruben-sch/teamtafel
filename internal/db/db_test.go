package db

import (
	"context"
	"os"
	"testing"
)

// testDatabaseURL liefert die URL der Testdatenbank. In CI ist sie Pflicht,
// lokal wird der Test ohne Datenbank übersprungen.
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL muss in CI gesetzt sein")
		}
		t.Skip("TEST_DATABASE_URL nicht gesetzt")
	}
	return url
}

func TestMigrateLegtVereinstabelleAn(t *testing.T) {
	url := testDatabaseURL(t)
	ctx := context.Background()

	// Zweimal ausführen: Migrationen müssen idempotent einspielbar sein.
	for i := 0; i < 2; i++ {
		if err := Migrate(ctx, url); err != nil {
			t.Fatalf("lauf %d: %v", i+1, err)
		}
	}

	pool, err := Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var exists bool
	err = pool.QueryRow(ctx, `SELECT to_regclass('public.verein') IS NOT NULL`).Scan(&exists)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("tabelle verein fehlt nach der migration")
	}
}

func TestConnectMeldetFehlerOhneDatenbank(t *testing.T) {
	_, err := Connect(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	if err == nil {
		t.Fatal("erwartet fehler bei nicht erreichbarer datenbank")
	}
}
