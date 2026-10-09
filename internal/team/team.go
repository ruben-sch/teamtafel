// Package team verwaltet Trainer, Spieler, Kader, Team-Links und Beitrittsanfragen.
package team

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruben-sch/teamtafel/internal/db"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

// Art einer Beitrittsanfrage.
const (
	ArtKind   = "kind"   // Elternteil meldet ein Kind an
	ArtSelbst = "selbst" // Person spielt selbst
)

var (
	// ErrEinladungUngueltig: Team-Link unbekannt, deaktiviert oder abgelaufen.
	ErrEinladungUngueltig = errors.New("team-link ungültig")
	// ErrAnfrageUnbekannt: keine offene Anfrage mit dieser ID.
	ErrAnfrageUnbekannt = errors.New("anfrage nicht gefunden")
	// ErrUngueltigeAngaben: Pflichtfelder fehlen oder Jahrgang unplausibel.
	ErrUngueltigeAngaben = errors.New("angaben unvollständig oder ungültig")
)

// Spieler ist eine Person im Verein.
type Spieler struct {
	ID       string
	Vorname  string
	Nachname string
	Jahrgang int
	// Selbst: Das abfragende Konto ist der Spieler selbst, nicht Vertretung.
	Selbst bool
}

// AnfrageDaten sind die Eingaben im Beitrittsformular.
type AnfrageDaten struct {
	Art      string
	Vorname  string
	Nachname string
	Jahrgang int
}

// Anfrage ist eine Beitrittsanfrage.
type Anfrage struct {
	ID string
	AnfrageDaten
	Email            string
	TrefferSpielerID string
	Erstellt         time.Time
}

// Store greift mit der App-Rolle auf die Datenbank zu; alles läuft in InVerein.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore erzeugt einen Store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) inVerein(ctx context.Context, vereinID string, fn func(pgx.Tx) error) error {
	return db.InVerein(ctx, s.pool, vereinID, fn)
}

// TrainerHinzufuegen macht ein Konto zum Trainer der Mannschaft.
func (s *Store) TrainerHinzufuegen(ctx context.Context, vereinID, mannschaftID, kontoID string) error {
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO trainer (verein_id, mannschaft_id, konto_id) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, vereinID, mannschaftID, kontoID)
		return err
	})
}

// IstTrainer prüft, ob das Konto Trainer der Mannschaft ist.
func (s *Store) IstTrainer(ctx context.Context, vereinID, mannschaftID, kontoID string) (bool, error) {
	var ok bool
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM trainer WHERE mannschaft_id = $1 AND konto_id = $2)`,
			mannschaftID, kontoID).Scan(&ok)
	})
	return ok, err
}

// TrainerMannschaften listet die Mannschaften, die das Konto trainiert.
func (s *Store) TrainerMannschaften(ctx context.Context, vereinID, kontoID string) ([]verein.Mannschaft, error) {
	var out []verein.Mannschaft
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT m.id::text, m.name, sa.name
FROM trainer t JOIN mannschaft m ON m.id = t.mannschaft_id JOIN saison sa ON sa.id = m.saison_id
WHERE t.konto_id = $1 ORDER BY sa.beginn DESC, m.name`, kontoID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (verein.Mannschaft, error) {
			var m verein.Mannschaft
			return m, r.Scan(&m.ID, &m.Name, &m.Saison)
		})
		return err
	})
	return out, err
}

// Mannschaft liefert eine Mannschaft des Vereins.
func (s *Store) Mannschaft(ctx context.Context, vereinID, mannschaftID string) (verein.Mannschaft, error) {
	var m verein.Mannschaft
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT m.id::text, m.name, sa.name FROM mannschaft m JOIN saison sa ON sa.id = m.saison_id
			WHERE m.id = $1`, mannschaftID).Scan(&m.ID, &m.Name, &m.Saison)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return m, verein.ErrNotFound
	}
	return m, err
}

// EinladungErneuern erzeugt einen neuen Team-Link und deaktiviert den bisherigen.
// Das Token gibt es nur hier im Klartext.
func (s *Store) EinladungErneuern(ctx context.Context, vereinID, mannschaftID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE einladung SET aktiv = false WHERE mannschaft_id = $1 AND aktiv`, mannschaftID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO einladung (verein_id, mannschaft_id, token_hash) VALUES ($1, $2, $3)`,
			vereinID, mannschaftID, hash(token))
		return err
	})
	if err != nil {
		return "", fmt.Errorf("team-link erneuern: %w", err)
	}
	return token, nil
}

// Einladung liefert die Mannschaft zu einem gültigen Team-Link.
func (s *Store) Einladung(ctx context.Context, vereinID, token string) (verein.Mannschaft, error) {
	var m verein.Mannschaft
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
SELECT m.id::text, m.name, sa.name
FROM einladung e JOIN mannschaft m ON m.id = e.mannschaft_id JOIN saison sa ON sa.id = m.saison_id
WHERE e.token_hash = $1 AND e.aktiv AND (e.gueltig_bis IS NULL OR e.gueltig_bis > now())`, hash(token)).
			Scan(&m.ID, &m.Name, &m.Saison)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrEinladungUngueltig
	}
	return m, err
}

// AnfrageStellen legt eine Beitrittsanfrage an und merkt einen möglichen
// Treffer vor: einen Spieler im Verein mit gleichem Namen und Jahrgang.
func (s *Store) AnfrageStellen(ctx context.Context, vereinID, mannschaftID, kontoID string, d AnfrageDaten) (Anfrage, error) {
	d.Vorname, d.Nachname = strings.TrimSpace(d.Vorname), strings.TrimSpace(d.Nachname)
	if (d.Art != ArtKind && d.Art != ArtSelbst) || d.Vorname == "" || d.Nachname == "" ||
		d.Jahrgang < 1900 || d.Jahrgang > time.Now().Year() {
		return Anfrage{}, ErrUngueltigeAngaben
	}
	a := Anfrage{AnfrageDaten: d}
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		var treffer *string
		err := tx.QueryRow(ctx, `
SELECT id::text FROM spieler
WHERE lower(vorname) = lower($1) AND lower(nachname) = lower($2) AND jahrgang = $3
ORDER BY created_at LIMIT 1`, d.Vorname, d.Nachname, d.Jahrgang).Scan(&treffer)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if treffer != nil {
			a.TrefferSpielerID = *treffer
		}
		return tx.QueryRow(ctx, `
INSERT INTO beitrittsanfrage (verein_id, mannschaft_id, konto_id, art, vorname, nachname, jahrgang, treffer_spieler_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (mannschaft_id, konto_id, lower(vorname), lower(nachname), jahrgang) WHERE status = 'offen'
DO UPDATE SET art = EXCLUDED.art
RETURNING id::text, created_at`, vereinID, mannschaftID, kontoID, d.Art, d.Vorname, d.Nachname, d.Jahrgang, treffer).
			Scan(&a.ID, &a.Erstellt)
	})
	if err != nil {
		return Anfrage{}, fmt.Errorf("anfrage stellen: %w", err)
	}
	return a, nil
}

// OffeneAnfragen listet die offenen Anfragen einer Mannschaft, älteste zuerst.
func (s *Store) OffeneAnfragen(ctx context.Context, vereinID, mannschaftID string) ([]Anfrage, error) {
	var out []Anfrage
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT a.id::text, a.art, a.vorname, a.nachname, a.jahrgang, k.email, coalesce(a.treffer_spieler_id::text, ''), a.created_at
FROM beitrittsanfrage a JOIN konto k ON k.id = a.konto_id
WHERE a.mannschaft_id = $1 AND a.status = 'offen' ORDER BY a.created_at`, mannschaftID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Anfrage, error) {
			var a Anfrage
			return a, r.Scan(&a.ID, &a.Art, &a.Vorname, &a.Nachname, &a.Jahrgang, &a.Email, &a.TrefferSpielerID, &a.Erstellt)
		})
		return err
	})
	return out, err
}

// AnfrageMannschaft liefert die Mannschaft einer offenen Anfrage (für die Berechtigungsprüfung).
func (s *Store) AnfrageMannschaft(ctx context.Context, vereinID, anfrageID string) (string, error) {
	var id string
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT mannschaft_id::text FROM beitrittsanfrage WHERE id = $1 AND status = 'offen'`,
			anfrageID).Scan(&id)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrAnfrageUnbekannt
	}
	return id, err
}

// Freigeben nimmt eine Anfrage an. Mit gleicherSpieler bestätigt der Trainer
// den vorgemerkten Treffer; sonst entsteht ein neuer Spieler.
func (s *Store) Freigeben(ctx context.Context, vereinID, anfrageID, trainerKontoID string, gleicherSpieler bool) error {
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		var a Anfrage
		var mannschaftID, kontoID string
		var treffer *string
		err := tx.QueryRow(ctx, `
SELECT mannschaft_id::text, konto_id::text, art, vorname, nachname, jahrgang, treffer_spieler_id::text
FROM beitrittsanfrage WHERE id = $1 AND status = 'offen' FOR UPDATE`, anfrageID).
			Scan(&mannschaftID, &kontoID, &a.Art, &a.Vorname, &a.Nachname, &a.Jahrgang, &treffer)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAnfrageUnbekannt
		}
		if err != nil {
			return err
		}

		var spielerID string
		if gleicherSpieler && treffer != nil {
			spielerID = *treffer
		} else {
			err = tx.QueryRow(ctx, `INSERT INTO spieler (verein_id, vorname, nachname, jahrgang) VALUES ($1, $2, $3, $4)
				RETURNING id::text`, vereinID, a.Vorname, a.Nachname, a.Jahrgang).Scan(&spielerID)
			if err != nil {
				return fmt.Errorf("spieler anlegen: %w", err)
			}
		}

		if a.Art == ArtSelbst {
			_, err = tx.Exec(ctx, `UPDATE spieler SET konto_id = $2 WHERE id = $1 AND konto_id IS NULL`, spielerID, kontoID)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO vertretung (verein_id, spieler_id, konto_id) VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING`, vereinID, spielerID, kontoID)
		}
		if err != nil {
			return fmt.Errorf("konto verknüpfen: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kader (verein_id, mannschaft_id, spieler_id) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, vereinID, mannschaftID, spielerID); err != nil {
			return fmt.Errorf("kader: %w", err)
		}
		_, err = tx.Exec(ctx, `UPDATE beitrittsanfrage SET status = 'freigegeben', entschieden_von = $2, entschieden_am = now()
			WHERE id = $1`, anfrageID, trainerKontoID)
		return err
	})
}

// Ablehnen löscht eine offene Anfrage samt eingegebener Daten.
func (s *Store) Ablehnen(ctx context.Context, vereinID, anfrageID string) error {
	return s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM beitrittsanfrage WHERE id = $1 AND status = 'offen'`, anfrageID)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrAnfrageUnbekannt
		}
		return err
	})
}

// Kader listet die Spieler einer Mannschaft.
func (s *Store) Kader(ctx context.Context, vereinID, mannschaftID string) ([]Spieler, error) {
	return s.spieler(ctx, vereinID, `
SELECT sp.id::text, sp.vorname, sp.nachname, sp.jahrgang, false
FROM kader k JOIN spieler sp ON sp.id = k.spieler_id
WHERE k.mannschaft_id = $1 ORDER BY sp.nachname, sp.vorname`, mannschaftID)
}

// MeineSpieler listet die Spieler, für die das Konto zu- und absagen darf:
// der eigene Spieler und vertretene Kinder.
func (s *Store) MeineSpieler(ctx context.Context, vereinID, kontoID string) ([]Spieler, error) {
	return s.spieler(ctx, vereinID, `
SELECT sp.id::text, sp.vorname, sp.nachname, sp.jahrgang, sp.konto_id IS NOT DISTINCT FROM $1::uuid
FROM spieler sp
WHERE sp.konto_id = $1 OR EXISTS (SELECT 1 FROM vertretung v WHERE v.spieler_id = sp.id AND v.konto_id = $1)
ORDER BY sp.vorname`, kontoID)
}

func (s *Store) spieler(ctx context.Context, vereinID, query string, arg string) ([]Spieler, error) {
	var out []Spieler
	err := s.inVerein(ctx, vereinID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, arg)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Spieler, error) {
			var sp Spieler
			return sp, r.Scan(&sp.ID, &sp.Vorname, &sp.Nachname, &sp.Jahrgang, &sp.Selbst)
		})
		return err
	})
	return out, err
}

func hash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
