package auth_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/dbtest"
)

type testMailer struct {
	mu     sync.Mutex
	an     []string
	text   string
	fehler error
}

func (m *testMailer) Senden(_ context.Context, an, _, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fehler != nil {
		return m.fehler
	}
	m.an, m.text = append(m.an, an), text
	return nil
}

func (m *testMailer) bekam(addr string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.an {
		if a == addr {
			return true
		}
	}
	return false
}

// neuesFristKonto legt ein Konto an und setzt die Fristspalten direkt; nil heißt NULL.
func neuesFristKonto(t *testing.T, pool *pgxpool.Pool, seit, hinweis *time.Time) auth.Konto {
	t.Helper()
	ctx := context.Background()
	k, err := auth.NewStore(pool).KontoFuer(ctx, email())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE konto SET unverknuepft_seit = $2, hinweis_am = $3 WHERE id = $1`,
		k.ID, seit, hinweis); err != nil {
		t.Fatal(err)
	}
	return k
}

func fristen(t *testing.T, pool *pgxpool.Pool, id string) (seit, hinweis *time.Time, vorhanden bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `SELECT unverknuepft_seit, hinweis_am FROM konto WHERE id = $1`, id).
		Scan(&seit, &hinweis)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, false
		}
		t.Fatal(err)
	}
	return seit, hinweis, true
}

func vor(d time.Duration) *time.Time {
	t := time.Now().Add(-d)
	return &t
}

const tag = 24 * time.Hour

func TestKontenOhneVerknuepfungWerdenGewarntUndGeloescht(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := context.Background()
	store := auth.NewStore(pool)

	neu := neuesFristKonto(t, pool, nil, nil)
	warnen := neuesFristKonto(t, pool, vor(24*tag), nil)
	loeschen := neuesFristKonto(t, pool, vor(31*tag), vor(8*tag))
	frischGewarnt := neuesFristKonto(t, pool, vor(31*tag), vor(2*tag))
	verknuepft := neuesFristKonto(t, pool, vor(31*tag), vor(8*tag))
	admin := neuesFristKonto(t, pool, vor(31*tag), vor(8*tag))

	m := &testMailer{}
	err := store.KontenAufraeumen(ctx, auth.KontoFrist{
		Verknuepft:   []string{verknuepft.ID},
		Geschuetzt:   []string{" " + strings.ToUpper(admin.Email) + " "},
		Mailer:       m,
		Erlaubt:      nil,
		Vollstaendig: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if seit, hinweis, ok := fristen(t, pool, neu.ID); !ok || seit == nil || hinweis != nil {
		t.Errorf("neu: seit %v, hinweis %v, vorhanden %v", seit, hinweis, ok)
	}
	if _, hinweis, ok := fristen(t, pool, warnen.ID); !ok || hinweis == nil {
		t.Errorf("warnen: hinweis %v, vorhanden %v", hinweis, ok)
	}
	if !m.bekam(warnen.Email) {
		t.Error("hinweis-mail fehlt")
	}
	if !strings.Contains(m.text, "gelöscht") || !strings.Contains(m.text, "Team-Link") {
		t.Errorf("mailtext: %q", m.text)
	}
	if _, _, ok := fristen(t, pool, loeschen.ID); ok {
		t.Error("abgelaufenes konto nicht gelöscht")
	}
	if _, _, ok := fristen(t, pool, frischGewarnt.ID); !ok {
		t.Error("vor ablauf der hinweisfrist gelöscht")
	}
	for name, k := range map[string]auth.Konto{"verknüpft": verknuepft, "admin": admin} {
		if seit, hinweis, ok := fristen(t, pool, k.ID); !ok || seit != nil || hinweis != nil {
			t.Errorf("%s: seit %v, hinweis %v, vorhanden %v", name, seit, hinweis, ok)
		}
	}
	for _, k := range []auth.Konto{neu, frischGewarnt, verknuepft, admin, loeschen} {
		if m.bekam(k.Email) {
			t.Errorf("unerwartete mail an %s", k.ID)
		}
	}
}

func TestKontoFristOhneVersandKeineLoeschung(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := context.Background()
	store := auth.NewStore(pool)

	// Nicht freigegebene Adresse (Staging) und Mailfehler: kein Hinweis, also auch keine Löschung.
	gesperrt := neuesFristKonto(t, pool, vor(40*tag), nil)
	m := &testMailer{}
	if err := store.KontenAufraeumen(ctx, auth.KontoFrist{Mailer: m, Erlaubt: []string{"jemand@example.org"},
		Vollstaendig: true}); err != nil {
		t.Fatal(err)
	}
	if _, hinweis, ok := fristen(t, pool, gesperrt.ID); !ok || hinweis != nil {
		t.Errorf("gesperrt: hinweis %v, vorhanden %v", hinweis, ok)
	}

	fehler := neuesFristKonto(t, pool, vor(40*tag), nil)
	m = &testMailer{fehler: errors.New("smtp kaputt")}
	if err := store.KontenAufraeumen(ctx, auth.KontoFrist{Mailer: m, Vollstaendig: true}); err != nil {
		t.Fatal(err)
	}
	if _, hinweis, ok := fristen(t, pool, fehler.ID); !ok || hinweis != nil {
		t.Errorf("mailfehler: hinweis %v, vorhanden %v", hinweis, ok)
	}
}

func TestKontoFristNurMitVollstaendigerPruefung(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := context.Background()
	k := neuesFristKonto(t, pool, vor(31*tag), vor(8*tag))
	if err := auth.NewStore(pool).KontenAufraeumen(ctx, auth.KontoFrist{Mailer: &testMailer{}}); err == nil {
		t.Error("kein fehler ohne vollständige prüfung")
	}
	if _, _, ok := fristen(t, pool, k.ID); !ok {
		t.Error("gelöscht trotz unvollständiger prüfung")
	}
}
