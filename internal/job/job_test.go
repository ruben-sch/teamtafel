package job_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruben-sch/teamtafel/internal/dbtest"
	"github.com/ruben-sch/teamtafel/internal/job"
)

// eigeneArt trennt die Jobs eines Tests von denen paralleler Testpakete.
func eigeneArt(t *testing.T) string {
	return fmt.Sprintf("test-%s-%d", t.Name(), time.Now().UnixNano())
}

func einreihen(t *testing.T, pool *pgxpool.Pool, art string, payload any, ab time.Time) {
	t.Helper()
	err := pgx.BeginFunc(context.Background(), pool, func(tx pgx.Tx) error {
		return job.Einreihen(context.Background(), tx, art, payload, ab)
	})
	if err != nil {
		t.Fatal(err)
	}
}

type stand struct {
	versuche              int
	erledigt, gescheitert bool
	faelligAb             time.Time
}

func standVon(t *testing.T, pool *pgxpool.Pool, art string) stand {
	t.Helper()
	var s stand
	err := pool.QueryRow(context.Background(), `
SELECT versuche, erledigt_am IS NOT NULL, fehlgeschlagen_am IS NOT NULL, faellig_ab FROM job WHERE art = $1`, art).
		Scan(&s.versuche, &s.erledigt, &s.gescheitert, &s.faelligAb)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestJobWirdMitNutzlastAbgearbeitet(t *testing.T) {
	pool := dbtest.AppPool(t)
	art := eigeneArt(t)
	jetzt := time.Now()
	einreihen(t, pool, art, map[string]string{"an": "x"}, jetzt.Add(time.Minute))

	w := job.NewWorker(pool)
	w.Now = func() time.Time { return jetzt }
	var bekommen string
	w.Registrieren(art, func(_ context.Context, p []byte) error { bekommen = string(p); return nil })

	if n, err := w.Abarbeiten(context.Background()); err != nil || n != 0 {
		t.Fatalf("vor Fälligkeit: n=%d err=%v", n, err)
	}
	w.Now = func() time.Time { return jetzt.Add(2 * time.Minute) }
	if n, err := w.Abarbeiten(context.Background()); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if bekommen != `{"an": "x"}` && bekommen != `{"an":"x"}` {
		t.Errorf("payload %q", bekommen)
	}
	if s := standVon(t, pool, art); !s.erledigt || s.versuche != 1 {
		t.Errorf("stand %+v", s)
	}
	if n, _ := w.Abarbeiten(context.Background()); n != 0 {
		t.Errorf("erledigter Job erneut verarbeitet")
	}
}

func TestFehlschlagMitBackoffBisAchtVersuche(t *testing.T) {
	pool := dbtest.AppPool(t)
	art := eigeneArt(t)
	jetzt := time.Now().Truncate(time.Second)
	einreihen(t, pool, art, 1, jetzt)

	w := job.NewWorker(pool)
	w.Now = func() time.Time { return jetzt }
	w.Registrieren(art, func(context.Context, []byte) error { return errors.New("smtp weg") })

	warten := w.Basis
	for v := 1; v < job.MaxVersuche; v++ {
		if n, err := w.Abarbeiten(context.Background()); err != nil || n != 1 {
			t.Fatalf("versuch %d: n=%d err=%v", v, n, err)
		}
		s := standVon(t, pool, art)
		if s.versuche != v || s.gescheitert || !s.faelligAb.Equal(jetzt.Add(warten)) {
			t.Fatalf("versuch %d: %+v, erwartet fällig %v", v, s, jetzt.Add(warten))
		}
		jetzt = s.faelligAb
		warten *= 2
	}
	if _, err := w.Abarbeiten(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := standVon(t, pool, art); !s.gescheitert || s.versuche != job.MaxVersuche {
		t.Errorf("nach %d Versuchen: %+v", job.MaxVersuche, s)
	}
}

func TestEndgueltigerFehlerOhneWiederholung(t *testing.T) {
	pool := dbtest.AppPool(t)
	art := eigeneArt(t)
	einreihen(t, pool, art, 1, time.Now())
	w := job.NewWorker(pool)
	w.Registrieren(art, func(context.Context, []byte) error { return job.Endgueltig(errors.New("konto gelöscht")) })
	if _, err := w.Abarbeiten(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := standVon(t, pool, art); !s.gescheitert || s.versuche != 1 {
		t.Errorf("stand %+v", s)
	}
}

func TestRollbackVerwirftJob(t *testing.T) {
	pool := dbtest.AppPool(t)
	art := eigeneArt(t)
	_ = pgx.BeginFunc(context.Background(), pool, func(tx pgx.Tx) error {
		if err := job.Einreihen(context.Background(), tx, art, 1, time.Now()); err != nil {
			t.Fatal(err)
		}
		return errors.New("fachlicher fehler")
	})
	var n int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM job WHERE art = $1`, art).Scan(&n)
	if n != 0 {
		t.Errorf("job trotz Rollback vorhanden")
	}
}

func TestParalleleWorkerOhneDoppelversand(t *testing.T) {
	pool := dbtest.AppPool(t)
	art := eigeneArt(t)
	const jobs = 20
	for i := range jobs {
		einreihen(t, pool, art, i, time.Now())
	}
	var mu sync.Mutex
	gesehen := map[string]int{}
	var gesamt atomic.Int64
	var wg sync.WaitGroup
	for range 4 {
		w := job.NewWorker(pool)
		w.Registrieren(art, func(_ context.Context, p []byte) error {
			mu.Lock()
			gesehen[string(p)]++
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			return nil
		})
		wg.Go(func() {
			n, err := w.Abarbeiten(context.Background())
			if err != nil {
				t.Error(err)
			}
			gesamt.Add(int64(n))
		})
	}
	wg.Wait()
	if gesamt.Load() != jobs || len(gesehen) != jobs {
		t.Fatalf("verarbeitet %d, verschieden %d", gesamt.Load(), len(gesehen))
	}
	for p, n := range gesehen {
		if n != 1 {
			t.Errorf("job %s %d-mal verarbeitet", p, n)
		}
	}
}

func TestFremdeArtBleibtLiegen(t *testing.T) {
	pool := dbtest.AppPool(t)
	fremd := eigeneArt(t)
	einreihen(t, pool, fremd, 1, time.Now())
	w := job.NewWorker(pool)
	w.Registrieren(eigeneArt(t)+"-x", func(context.Context, []byte) error { return nil })
	if _, err := w.Abarbeiten(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := standVon(t, pool, fremd); s.erledigt || s.versuche != 0 {
		t.Errorf("fremder Job angefasst: %+v", s)
	}
}
