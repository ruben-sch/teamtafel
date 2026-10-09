// Package dbtest stellt Integrationstests eine migrierte Datenbank bereit.
//
// Migrationen laufen mit TEST_DATABASE_URL (Owner/Superuser). Die Tests selbst
// verbinden sich wie die App mit einer Rolle ohne BYPASSRLS, damit
// Row-Level-Security tatsächlich greift.
package dbtest

import (
	"context"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruben-sch/teamtafel/internal/db"
)

const (
	appRole     = "teamtafel_test_app"
	appPassword = "teamtafel_test_app"
)

var setupOnce sync.Once
var setupErr error

// URL liefert TEST_DATABASE_URL. In CI ist sie Pflicht, lokal wird der Test
// ohne Datenbank übersprungen.
func URL(t testing.TB) string {
	t.Helper()
	u := os.Getenv("TEST_DATABASE_URL")
	if u == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL muss in CI gesetzt sein")
		}
		t.Skip("TEST_DATABASE_URL nicht gesetzt")
	}
	return u
}

// AppPool migriert die Testdatenbank einmal pro Testlauf und liefert einen
// Pool, der als App-Rolle ohne BYPASSRLS verbunden ist.
func AppPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ownerURL := URL(t)
	ctx := context.Background()

	setupOnce.Do(func() { setupErr = setup(ctx, ownerURL) })
	if setupErr != nil {
		t.Fatalf("testdatenbank vorbereiten: %v", setupErr)
	}

	u, err := url.Parse(ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(appRole, appPassword)
	pool, err := db.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func setup(ctx context.Context, ownerURL string) error {
	if err := db.Migrate(ctx, ownerURL); err != nil {
		return err
	}
	owner, err := db.Connect(ctx, ownerURL)
	if err != nil {
		return err
	}
	defer owner.Close()

	// Entspricht deploy/initdb/01-app-role.sh, aber idempotent und nach den
	// Migrationen, daher Rechte auf bestehende Tabellen statt Default-Privileges.
	_, err = owner.Exec(ctx, `
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '`+appRole+`') THEN
    CREATE ROLE `+appRole+` LOGIN PASSWORD '`+appPassword+`' NOSUPERUSER NOBYPASSRLS;
  END IF;
END $$;
GRANT USAGE ON SCHEMA public TO `+appRole+`;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO `+appRole+`;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO `+appRole+`;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO `+appRole+`;
`)
	return err
}
