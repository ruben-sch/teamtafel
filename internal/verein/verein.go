// Package verein verwaltet Mandanten (Vereine), Saisons und Mannschaften.
package verein

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruben-sch/teamtafel/internal/db"
)

var (
	// ErrNotFound: kein Verein mit diesem Slug.
	ErrNotFound = errors.New("verein nicht gefunden")
	// ErrUngueltigerSlug: Slug taugt nicht als Subdomain oder ist reserviert.
	ErrUngueltigerSlug = errors.New("ungültiger slug")
	// ErrUngueltigerName: Name fehlt.
	ErrUngueltigerName = errors.New("name fehlt")
	// ErrSlugVergeben: Es gibt schon einen Verein mit diesem Slug.
	ErrSlugVergeben = errors.New("slug ist schon vergeben")
	// ErrMannschaftVorhanden: Name in dieser Saison schon vergeben.
	ErrMannschaftVorhanden = errors.New("mannschaft gibt es in der saison schon")
	// ErrUngueltigeSaison: Saisonname nicht im Format JJJJ/JJ.
	ErrUngueltigeSaison = errors.New("saison muss das format JJJJ/JJ haben, z. B. 2026/27")
)

var (
	slugPattern   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	saisonPattern = regexp.MustCompile(`^(\d{4})/(\d{2})$`)
	// Reserviert, weil sie als Subdomain mit Umgebungen oder Diensten kollidieren.
	reserviert = map[string]bool{"www": true, "staging": true, "api": true, "admin": true, "plattform": true, "mail": true}
)

// Verein ist ein Mandant.
type Verein struct {
	ID   string
	Slug string
	Name string
}

// Mannschaft gehört zu einer Saison eines Vereins.
type Mannschaft struct {
	ID     string
	Name   string
	Saison string
}

// Store greift mit der App-Rolle auf die Datenbank zu.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore erzeugt einen Store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Anlegen legt einen Verein an. Der Slug wird zur Subdomain.
func (s *Store) Anlegen(ctx context.Context, slug, name string) (Verein, error) {
	if !slugPattern.MatchString(slug) || reserviert[slug] {
		return Verein{}, fmt.Errorf("%w: %q", ErrUngueltigerSlug, slug)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Verein{}, ErrUngueltigerName
	}
	v := Verein{Slug: slug, Name: name}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO verein (slug, name) VALUES ($1, $2) ON CONFLICT (slug) DO NOTHING RETURNING id::text`, slug, name).Scan(&v.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Verein{}, fmt.Errorf("%w: %q", ErrSlugVergeben, slug)
	}
	if err != nil {
		return Verein{}, fmt.Errorf("verein anlegen: %w", err)
	}
	return v, nil
}

// BySlug sucht einen Verein anhand seines Slugs.
func (s *Store) BySlug(ctx context.Context, slug string) (Verein, error) {
	v := Verein{Slug: slug}
	err := s.pool.QueryRow(ctx, `SELECT id::text, name FROM verein WHERE slug = $1`, slug).Scan(&v.ID, &v.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Verein{}, ErrNotFound
	}
	if err != nil {
		return Verein{}, fmt.Errorf("verein suchen: %w", err)
	}
	return v, nil
}

// Alle listet alle Vereine, für die Plattform-Verwaltung.
func (s *Store) Alle(ctx context.Context) ([]Verein, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, slug, name FROM verein ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("vereine laden: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Verein, error) {
		var v Verein
		return v, r.Scan(&v.ID, &v.Slug, &v.Name)
	})
}

// AdminHinzufuegen macht ein Konto zum Admin des Vereins.
func (s *Store) AdminHinzufuegen(ctx context.Context, vereinID, kontoID string) error {
	return db.InVerein(ctx, s.pool, vereinID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO verein_admin (verein_id, konto_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			vereinID, kontoID)
		return err
	})
}

// IstAdmin prüft, ob das Konto Admin des Vereins ist.
func (s *Store) IstAdmin(ctx context.Context, vereinID, kontoID string) (bool, error) {
	var ok bool
	err := db.InVerein(ctx, s.pool, vereinID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM verein_admin WHERE konto_id = $1)`, kontoID).Scan(&ok)
	})
	return ok, err
}

// Admins listet die E-Mail-Adressen der Vereinsadmins.
func (s *Store) Admins(ctx context.Context, vereinID string) ([]string, error) {
	var out []string
	err := db.InVerein(ctx, s.pool, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT k.email FROM verein_admin a JOIN konto k ON k.id = a.konto_id ORDER BY k.email`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return out, err
}

// MannschaftAnlegen legt eine Mannschaft an; die Saison entsteht bei Bedarf.
func (s *Store) MannschaftAnlegen(ctx context.Context, vereinID, saison, name string) (Mannschaft, error) {
	beginn, ende, err := saisonZeitraum(saison)
	if err != nil {
		return Mannschaft{}, err
	}
	m := Mannschaft{Name: name, Saison: saison}
	err = db.InVerein(ctx, s.pool, vereinID, func(tx pgx.Tx) error {
		var saisonID string
		err := tx.QueryRow(ctx, `
INSERT INTO saison (verein_id, name, beginn, ende) VALUES ($1, $2, $3, $4)
ON CONFLICT (verein_id, name) DO UPDATE SET name = EXCLUDED.name
RETURNING id::text`, vereinID, saison, beginn, ende).Scan(&saisonID)
		if err != nil {
			return fmt.Errorf("saison anlegen: %w", err)
		}
		err = tx.QueryRow(ctx, `
INSERT INTO mannschaft (verein_id, saison_id, name) VALUES ($1, $2, $3)
RETURNING id::text`, vereinID, saisonID, name).Scan(&m.ID)
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
			return ErrMannschaftVorhanden
		}
		if err != nil {
			return fmt.Errorf("mannschaft anlegen: %w", err)
		}
		return nil
	})
	return m, err
}

// Mannschaften listet die Mannschaften eines Vereins, neueste Saison zuerst.
func (s *Store) Mannschaften(ctx context.Context, vereinID string) ([]Mannschaft, error) {
	var out []Mannschaft
	err := db.InVerein(ctx, s.pool, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT m.id::text, m.name, s.name
FROM mannschaft m JOIN saison s ON s.id = m.saison_id
ORDER BY s.beginn DESC, m.name`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Mannschaft, error) {
			var m Mannschaft
			err := r.Scan(&m.ID, &m.Name, &m.Saison)
			return m, err
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("mannschaften laden: %w", err)
	}
	return out, nil
}

// saisonZeitraum leitet aus "2026/27" den Zeitraum 1.7.2026 bis 30.6.2027 ab.
func saisonZeitraum(name string) (time.Time, time.Time, error) {
	m := saisonPattern.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, time.Time{}, ErrUngueltigeSaison
	}
	start, _ := strconv.Atoi(m[1])
	if (start+1)%100 != mustAtoi(m[2]) {
		return time.Time{}, time.Time{}, ErrUngueltigeSaison
	}
	beginn := time.Date(start, time.July, 1, 0, 0, 0, 0, time.UTC)
	return beginn, beginn.AddDate(1, 0, -1), nil
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
