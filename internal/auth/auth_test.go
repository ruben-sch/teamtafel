package auth_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/dbtest"
)

func email() string {
	return fmt.Sprintf("Person.%d@Example.org", time.Now().UnixNano())
}

func TestLinkEinloesenLegtKontoUndSessionAn(t *testing.T) {
	store := auth.NewStore(dbtest.AppPool(t))
	ctx := context.Background()
	addr := email()

	token, err := store.LinkAnfordern(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) < 40 {
		t.Fatalf("token zu kurz: %q", token)
	}

	sess, konto, err := store.Einloesen(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if konto.Email != normalisiert(addr) {
		t.Fatalf("email = %q, want %q", konto.Email, normalisiert(addr))
	}

	got, err := store.Sitzung(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != konto.ID {
		t.Fatalf("sitzung gehört zu %s, want %s", got.ID, konto.ID)
	}

	// Zweiter Login mit derselben Adresse (andere Schreibweise) nutzt dasselbe Konto.
	token2, _ := store.LinkAnfordern(ctx, " "+normalisiert(addr)+" ")
	_, konto2, err := store.Einloesen(ctx, token2)
	if err != nil {
		t.Fatal(err)
	}
	if konto2.ID != konto.ID {
		t.Fatal("zweiter login muss dasselbe konto liefern")
	}
}

func TestLinkIstNurEinmalGueltig(t *testing.T) {
	store := auth.NewStore(dbtest.AppPool(t))
	ctx := context.Background()
	token, _ := store.LinkAnfordern(ctx, email())
	if _, _, err := store.Einloesen(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Einloesen(ctx, token); !errors.Is(err, auth.ErrUngueltig) {
		t.Fatalf("zweites einlösen: err = %v, want ErrUngueltig", err)
	}
	if _, _, err := store.Einloesen(ctx, "unsinn"); !errors.Is(err, auth.ErrUngueltig) {
		t.Fatalf("falsches token: err = %v, want ErrUngueltig", err)
	}
}

func TestAbgelaufenerLinkUndAbgelaufeneSession(t *testing.T) {
	jetzt := time.Now()
	store := auth.NewStore(dbtest.AppPool(t))
	store.Now = func() time.Time { return jetzt }
	ctx := context.Background()

	token, _ := store.LinkAnfordern(ctx, email())
	jetzt = jetzt.Add(16 * time.Minute)
	if _, _, err := store.Einloesen(ctx, token); !errors.Is(err, auth.ErrUngueltig) {
		t.Fatalf("nach 16 minuten: err = %v, want ErrUngueltig", err)
	}

	token, _ = store.LinkAnfordern(ctx, email())
	sess, _, err := store.Einloesen(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	// Gleitend: Nutzung nach 60 Tagen verlängert um weitere 90 Tage.
	jetzt = jetzt.Add(60 * 24 * time.Hour)
	if _, err := store.Sitzung(ctx, sess); err != nil {
		t.Fatalf("nach 60 tagen: %v", err)
	}
	jetzt = jetzt.Add(89 * 24 * time.Hour)
	if _, err := store.Sitzung(ctx, sess); err != nil {
		t.Fatalf("89 tage nach letzter nutzung: %v", err)
	}
	jetzt = jetzt.Add(91 * 24 * time.Hour)
	if _, err := store.Sitzung(ctx, sess); !errors.Is(err, auth.ErrUngueltig) {
		t.Fatalf("91 tage ohne nutzung: err = %v, want ErrUngueltig", err)
	}
}

func TestAbmeldenBeendetSession(t *testing.T) {
	store := auth.NewStore(dbtest.AppPool(t))
	ctx := context.Background()
	token, _ := store.LinkAnfordern(ctx, email())
	sess, _, _ := store.Einloesen(ctx, token)
	if err := store.Abmelden(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Sitzung(ctx, sess); !errors.Is(err, auth.ErrUngueltig) {
		t.Fatalf("nach abmelden: err = %v, want ErrUngueltig", err)
	}
}

func TestTokensWerdenNurAlsHashGespeichert(t *testing.T) {
	pool := dbtest.AppPool(t)
	store := auth.NewStore(pool)
	ctx := context.Background()
	token, _ := store.LinkAnfordern(ctx, email())
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM login_token WHERE encode(token_hash, 'escape') = $1 OR encode(token_hash, 'hex') = $1`, token).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("klartext-token in der datenbank")
	}
}

func TestUngueltigeAdresse(t *testing.T) {
	store := auth.NewStore(dbtest.AppPool(t))
	for _, a := range []string{"", "keine-adresse", "a@", "@b.de"} {
		if _, err := store.LinkAnfordern(context.Background(), a); !errors.Is(err, auth.ErrUngueltigeAdresse) {
			t.Errorf("%q: err = %v, want ErrUngueltigeAdresse", a, err)
		}
	}
}

func normalisiert(s string) string { return auth.NormalisiereEmail(s) }
