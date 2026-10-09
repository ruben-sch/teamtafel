// Package auth implementiert den Login per Magic-Link und serverseitige Sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// LinkGueltigkeit: so lange ist ein Magic-Link einlösbar.
	LinkGueltigkeit = 15 * time.Minute
	// SessionGueltigkeit gilt gleitend ab der letzten Nutzung.
	SessionGueltigkeit = 90 * 24 * time.Hour
	// Verlängert wird höchstens einmal am Tag, um Schreibzugriffe zu sparen.
	verlaengernNach = 24 * time.Hour
)

var (
	// ErrUngueltig: Token unbekannt, abgelaufen oder schon verwendet.
	ErrUngueltig = errors.New("token ungültig oder abgelaufen")
	// ErrUngueltigeAdresse: keine brauchbare E-Mail-Adresse.
	ErrUngueltigeAdresse = errors.New("ungültige e-mail-adresse")
)

// Konto ist eine Person mit Login.
type Konto struct {
	ID    string
	Email string
	Name  string
}

// Store verwaltet Login-Tokens und Sessions.
type Store struct {
	pool *pgxpool.Pool
	// Now ist austauschbar für Tests.
	Now func() time.Time
}

// NewStore erzeugt einen Store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, Now: time.Now}
}

// NormalisiereEmail entfernt Leerraum und vereinheitlicht die Schreibweise.
func NormalisiereEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// LinkAnfordern erzeugt ein Login-Token für die Adresse. Ob es ein Konto gibt,
// spielt keine Rolle: Die Antwort ist für bekannte und unbekannte Adressen gleich.
func (s *Store) LinkAnfordern(ctx context.Context, email string) (string, error) {
	email = NormalisiereEmail(email)
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		return "", ErrUngueltigeAdresse
	}
	token, hash, err := neuesToken()
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO login_token (token_hash, email, ablauf) VALUES ($1, $2, $3)`,
		hash, email, s.Now().Add(LinkGueltigkeit))
	if err != nil {
		return "", fmt.Errorf("login-token speichern: %w", err)
	}
	return token, nil
}

// Einloesen verbraucht ein Login-Token, legt bei Bedarf das Konto an und
// startet eine Session. Zurück kommt das Session-Token für das Cookie.
func (s *Store) Einloesen(ctx context.Context, token string) (string, Konto, error) {
	now := s.Now()
	var sess string
	var k Konto
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
UPDATE login_token SET verwendet_am = $2
WHERE token_hash = $1 AND verwendet_am IS NULL AND ablauf > $2
RETURNING email`, hashToken(token), now).Scan(&k.Email)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUngueltig
		}
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
INSERT INTO konto (email) VALUES ($1)
ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
RETURNING id::text, name`, k.Email).Scan(&k.ID, &k.Name)
		if err != nil {
			return fmt.Errorf("konto anlegen: %w", err)
		}
		var hash []byte
		sess, hash, err = neuesToken()
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO session (token_hash, konto_id, ablauf) VALUES ($1, $2, $3)`,
			hash, k.ID, now.Add(SessionGueltigkeit))
		return err
	})
	if err != nil {
		return "", Konto{}, err
	}
	return sess, k, nil
}

// Sitzung liefert das Konto einer gültigen Session und verlängert sie gleitend.
func (s *Store) Sitzung(ctx context.Context, token string) (Konto, error) {
	now := s.Now()
	hash := hashToken(token)
	var k Konto
	var ablauf time.Time
	err := s.pool.QueryRow(ctx, `
SELECT k.id::text, k.email, k.name, s.ablauf
FROM session s JOIN konto k ON k.id = s.konto_id
WHERE s.token_hash = $1 AND s.ablauf > $2`, hash, now).Scan(&k.ID, &k.Email, &k.Name, &ablauf)
	if errors.Is(err, pgx.ErrNoRows) {
		return Konto{}, ErrUngueltig
	}
	if err != nil {
		return Konto{}, fmt.Errorf("session laden: %w", err)
	}
	if neu := now.Add(SessionGueltigkeit); neu.Sub(ablauf) > verlaengernNach {
		if _, err := s.pool.Exec(ctx, `UPDATE session SET ablauf = $2 WHERE token_hash = $1`, hash, neu); err != nil {
			return Konto{}, fmt.Errorf("session verlängern: %w", err)
		}
	}
	return k, nil
}

// Abmelden beendet eine Session.
func (s *Store) Abmelden(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM session WHERE token_hash = $1`, hashToken(token))
	return err
}

// neuesToken erzeugt 32 Zufallsbytes, URL-tauglich kodiert, und ihren Hash.
func neuesToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
