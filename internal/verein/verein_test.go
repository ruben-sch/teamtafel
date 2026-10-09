package verein_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ruben-sch/teamtafel/internal/db"
	"github.com/ruben-sch/teamtafel/internal/dbtest"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

// eindeutigerSlug vermeidet Kollisionen, weil Tests sich eine Datenbank teilen.
func eindeutigerSlug(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func TestAnlegenUndFindenPerSlug(t *testing.T) {
	store := verein.NewStore(dbtest.AppPool(t))
	ctx := context.Background()
	slug := eindeutigerSlug("fc")

	v, err := store.Anlegen(ctx, slug, "FC Beispiel")
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.BySlug(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	if got != v || got.Name != "FC Beispiel" {
		t.Fatalf("got %+v, want %+v", got, v)
	}

	if _, err := store.BySlug(ctx, "gibt-es-nicht"); !errors.Is(err, verein.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := store.Anlegen(ctx, slug, "Doppelt"); err == nil {
		t.Fatal("doppelter slug muss fehlschlagen")
	}
}

func TestAnlegenPrueftSlug(t *testing.T) {
	store := verein.NewStore(dbtest.AppPool(t))
	for _, slug := range []string{"", "FC", "fc_x", "-fc", "fc-", "a.b", "staging", "www"} {
		if _, err := store.Anlegen(context.Background(), slug, "X"); !errors.Is(err, verein.ErrUngueltigerSlug) {
			t.Errorf("slug %q: err = %v, want ErrUngueltigerSlug", slug, err)
		}
	}
}

func TestMannschaftenSindProVereinGetrennt(t *testing.T) {
	pool := dbtest.AppPool(t)
	store := verein.NewStore(pool)
	ctx := context.Background()

	a, err := store.Anlegen(ctx, eindeutigerSlug("a"), "Verein A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Anlegen(ctx, eindeutigerSlug("b"), "Verein B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MannschaftAnlegen(ctx, a.ID, "2026/27", "Bambini A"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MannschaftAnlegen(ctx, b.ID, "2026/27", "Bambini B"); err != nil {
		t.Fatal(err)
	}
	// Zweite Mannschaft in derselben Saison legt keine zweite Saison an.
	if _, err := store.MannschaftAnlegen(ctx, a.ID, "2026/27", "F-Jugend A"); err != nil {
		t.Fatal(err)
	}

	got, err := store.Mannschaften(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Bambini A" || got[1].Name != "F-Jugend A" {
		t.Fatalf("mannschaften von A = %+v", got)
	}
	if got[0].Saison != "2026/27" {
		t.Fatalf("saison = %q", got[0].Saison)
	}

	// Ohne WHERE verein_id: RLS muss trotzdem nur Zeilen von A liefern.
	for _, table := range []string{"saison", "mannschaft"} {
		err := db.InVerein(ctx, pool, a.ID, func(tx pgx.Tx) error {
			var fremd int
			q := `SELECT count(*) FROM ` + table + ` WHERE verein_id <> $1`
			if err := tx.QueryRow(ctx, q, a.ID).Scan(&fremd); err != nil {
				return err
			}
			if fremd != 0 {
				t.Errorf("%s: verein A sieht %d fremde zeilen", table, fremd)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestOhneVereinSiehtManNichts(t *testing.T) {
	pool := dbtest.AppPool(t)
	store := verein.NewStore(pool)
	ctx := context.Background()
	a, err := store.Anlegen(ctx, eindeutigerSlug("leer"), "Verein")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MannschaftAnlegen(ctx, a.ID, "2026/27", "Bambini"); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM mannschaft`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("ohne gesetzten verein sichtbar: %d zeilen", n)
	}
}

func TestSchreibenInFremdenVereinScheitert(t *testing.T) {
	pool := dbtest.AppPool(t)
	store := verein.NewStore(pool)
	ctx := context.Background()
	a, _ := store.Anlegen(ctx, eindeutigerSlug("a"), "A")
	b, _ := store.Anlegen(ctx, eindeutigerSlug("b"), "B")

	err := db.InVerein(ctx, pool, a.ID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO saison (verein_id, name, beginn, ende)
			VALUES ($1, 'x', '2026-07-01', '2027-06-30')`, b.ID)
		return err
	})
	if err == nil {
		t.Fatal("insert mit fremder verein_id muss an der policy scheitern")
	}
}

// TestJedeVereinstabelleHatRLS fängt neue Tabellen ab, bei denen die Policy vergessen wurde.
func TestJedeVereinstabelleHatRLS(t *testing.T) {
	pool := dbtest.AppPool(t)
	rows, err := pool.Query(context.Background(), `
SELECT c.relname, c.relrowsecurity,
       EXISTS (SELECT 1 FROM pg_policies p
               WHERE p.schemaname = 'public' AND p.tablename = c.relname
                 AND p.qual LIKE '%current_setting(''app.verein_id''%'
                 AND p.with_check LIKE '%current_setting(''app.verein_id''%')
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'public'
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'verein_id' AND NOT a.attisdropped
WHERE c.relkind = 'r'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var name string
		var rls, policy bool
		if err := rows.Scan(&name, &rls, &policy); err != nil {
			t.Fatal(err)
		}
		count++
		if !rls || !policy {
			t.Errorf("tabelle %s: rls=%v policy=%v", name, rls, policy)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count < 2 {
		t.Fatalf("nur %d vereinstabellen gefunden, erwartet mindestens saison und mannschaft", count)
	}
}

func TestVereinsadmins(t *testing.T) {
	pool := dbtest.AppPool(t)
	store := verein.NewStore(pool)
	ctx := context.Background()
	v, err := store.Anlegen(ctx, eindeutigerSlug("adm"), "SV Admin")
	if err != nil {
		t.Fatal(err)
	}
	var kontoID string
	email := eindeutigerSlug("admin") + "@example.org"
	if err := pool.QueryRow(ctx, `INSERT INTO konto (email) VALUES ($1) RETURNING id::text`, email).Scan(&kontoID); err != nil {
		t.Fatal(err)
	}

	if ok, err := store.IstAdmin(ctx, v.ID, kontoID); err != nil || ok {
		t.Fatalf("vor dem eintragen: %v %v", ok, err)
	}
	if err := store.AdminHinzufuegen(ctx, v.ID, kontoID); err != nil {
		t.Fatal(err)
	}
	if err := store.AdminHinzufuegen(ctx, v.ID, kontoID); err != nil {
		t.Fatalf("zweimal eintragen: %v", err)
	}
	if ok, err := store.IstAdmin(ctx, v.ID, kontoID); err != nil || !ok {
		t.Fatalf("nach dem eintragen: %v %v", ok, err)
	}
	admins, err := store.Admins(ctx, v.ID)
	if err != nil || len(admins) != 1 || admins[0] != email {
		t.Fatalf("admins = %v %v", admins, err)
	}

	// Admin eines Vereins ist nicht Admin eines anderen.
	w, _ := store.Anlegen(ctx, eindeutigerSlug("adm"), "SV Anders")
	if ok, _ := store.IstAdmin(ctx, w.ID, kontoID); ok {
		t.Fatal("admin gilt vereinsübergreifend")
	}
}

func TestAlleVereineUndDoppelterSlug(t *testing.T) {
	store := verein.NewStore(dbtest.AppPool(t))
	ctx := context.Background()
	slug := eindeutigerSlug("alle")
	if _, err := store.Anlegen(ctx, slug, "FC Alle"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Anlegen(ctx, slug, "FC Doppelt"); !errors.Is(err, verein.ErrSlugVergeben) {
		t.Fatalf("err = %v, want ErrSlugVergeben", err)
	}
	alle, err := store.Alle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range alle {
		if v.Slug == slug {
			return
		}
	}
	t.Fatalf("%s fehlt in %v", slug, alle)
}

func TestMannschaftDoppeltInSaison(t *testing.T) {
	store := verein.NewStore(dbtest.AppPool(t))
	ctx := context.Background()
	v, _ := store.Anlegen(ctx, eindeutigerSlug("dop"), "FC Doppelt")
	if _, err := store.MannschaftAnlegen(ctx, v.ID, "2026/27", "F1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MannschaftAnlegen(ctx, v.ID, "2026/27", "F1"); !errors.Is(err, verein.ErrMannschaftVorhanden) {
		t.Fatalf("err = %v, want ErrMannschaftVorhanden", err)
	}
}

func TestVereinsfarbe(t *testing.T) {
	store := verein.NewStore(dbtest.AppPool(t))
	ctx := context.Background()
	slug := eindeutigerSlug("farbe")
	v, err := store.Anlegen(ctx, slug, "TTC Farbe")
	if err != nil {
		t.Fatal(err)
	}
	if v.Farbe != verein.StandardFarbe {
		t.Fatalf("neuer verein hat farbe %q", v.Farbe)
	}

	if err := store.FarbeSetzen(ctx, v.ID, "#8c1d2a"); err != nil {
		t.Fatal(err)
	}
	got, err := store.BySlug(ctx, slug)
	if err != nil || got.Farbe != "#8C1D2A" {
		t.Fatalf("farbe nach dem setzen = %q %v", got.Farbe, err)
	}

	for _, f := range []string{"", "rot", "#12345", "#GGGGGG", "#FFFFFF", "#F5D90A"} {
		if err := store.FarbeSetzen(ctx, v.ID, f); !errors.Is(err, verein.ErrUngueltigeFarbe) {
			t.Errorf("farbe %q: err = %v", f, err)
		}
	}
}
