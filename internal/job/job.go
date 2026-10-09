// Package job ist Outbox und Job-Queue in PostgreSQL.
//
// Jobs entstehen per Einreihen in derselben Transaktion wie die fachliche
// Änderung. Der Worker holt fällige Jobs mit FOR UPDATE SKIP LOCKED, sodass
// mehrere Instanzen ohne Doppelversand laufen können.
package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaxVersuche: danach gilt ein Job als fehlgeschlagen.
const MaxVersuche = 8

// Handler verarbeitet die Nutzlast eines Jobs.
type Handler func(ctx context.Context, payload []byte) error

// errEndgueltig markiert Fehler, bei denen ein neuer Versuch nichts ändert.
var errEndgueltig = errors.New("endgültig")

// Endgueltig verpackt err so, dass der Job sofort als fehlgeschlagen gilt.
func Endgueltig(err error) error { return fmt.Errorf("%w: %w", errEndgueltig, err) }

// Einreihen legt einen Job in der Transaktion tx an.
func Einreihen(ctx context.Context, tx pgx.Tx, art string, payload any, faelligAb time.Time) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("job %s: %w", art, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO job (art, payload, faellig_ab) VALUES ($1, $2, $3)`, art, b, faelligAb); err != nil {
		return fmt.Errorf("job %s einreihen: %w", art, err)
	}
	return nil
}

// Worker arbeitet Jobs der registrierten Arten ab; andere Arten lässt er liegen.
type Worker struct {
	pool     *pgxpool.Pool
	handlers map[string]Handler
	// Basis ist die Wartezeit nach dem ersten Fehlschlag; sie verdoppelt sich je Versuch.
	Basis time.Duration
	Now   func() time.Time
}

// NewWorker erzeugt einen Worker ohne Handler.
func NewWorker(pool *pgxpool.Pool) *Worker {
	return &Worker{pool: pool, handlers: map[string]Handler{}, Basis: 30 * time.Second, Now: time.Now}
}

// Registrieren ordnet einer Job-Art ihren Handler zu.
func (w *Worker) Registrieren(art string, h Handler) { w.handlers[art] = h }

// Abarbeiten verarbeitet alle fälligen Jobs und liefert ihre Anzahl.
func (w *Worker) Abarbeiten(ctx context.Context) (int, error) {
	n := 0
	for {
		ok, err := w.einen(ctx)
		if err != nil || !ok {
			return n, err
		}
		n++
	}
}

// einen holt und verarbeitet höchstens einen Job. Die Zeile bleibt während des
// Handlers gesperrt, damit keine andere Instanz ihn parallel ausführt.
func (w *Worker) einen(ctx context.Context) (bool, error) {
	arten := make([]string, 0, len(w.handlers))
	for a := range w.handlers {
		arten = append(arten, a)
	}
	gefunden := false
	err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		var id, art string
		var payload []byte
		var versuche int
		err := tx.QueryRow(ctx, `
SELECT id::text, art, payload, versuche FROM job
WHERE erledigt_am IS NULL AND fehlgeschlagen_am IS NULL AND faellig_ab <= $1 AND art = ANY($2)
ORDER BY faellig_ab LIMIT 1
FOR UPDATE SKIP LOCKED`, w.Now(), arten).Scan(&id, &art, &payload, &versuche)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		gefunden = true
		jetzt := w.Now()
		herr := w.handlers[art](ctx, payload)
		if herr == nil {
			_, err = tx.Exec(ctx, `UPDATE job SET erledigt_am = $2, versuche = versuche + 1 WHERE id = $1`, id, jetzt)
			return err
		}
		versuche++
		slog.Warn("job fehlgeschlagen", "art", art, "id", id, "versuch", versuche, "err", herr)
		if versuche >= MaxVersuche || errors.Is(herr, errEndgueltig) {
			_, err = tx.Exec(ctx, `UPDATE job SET versuche = $2, letzter_fehler = $3, fehlgeschlagen_am = $4 WHERE id = $1`,
				id, versuche, herr.Error(), jetzt)
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE job SET versuche = $2, letzter_fehler = $3, faellig_ab = $4 WHERE id = $1`,
			id, versuche, herr.Error(), jetzt.Add(w.Basis<<(versuche-1)))
		return err
	})
	if err != nil {
		return false, fmt.Errorf("job abarbeiten: %w", err)
	}
	return gefunden, nil
}

// Aufraeumen löscht erledigte Jobs, die älter als alter sind.
func (w *Worker) Aufraeumen(ctx context.Context, alter time.Duration) error {
	_, err := w.pool.Exec(ctx, `DELETE FROM job WHERE erledigt_am < $1`, w.Now().Add(-alter))
	return err
}

// Laufen arbeitet im Takt intervall ab, bis ctx endet.
func (w *Worker) Laufen(ctx context.Context, intervall time.Duration) {
	for {
		if _, err := w.Abarbeiten(ctx); err != nil && ctx.Err() == nil {
			slog.Error("jobs", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(intervall):
		}
	}
}
