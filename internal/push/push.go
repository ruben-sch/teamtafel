// Package push verwaltet Web-Push-Abos und verschickt Nachrichten per VAPID.
package push

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUngueltig: Abo unvollständig oder Endpoint kein https.
var ErrUngueltig = errors.New("push-abo ungültig")

// ErrAbgelaufen: Der Push-Dienst kennt das Abo nicht mehr (404/410); es sollte gelöscht werden.
var ErrAbgelaufen = errors.New("push-abo abgelaufen")

// Abo ist das Abo eines Geräts, wie es PushManager.subscribe() liefert.
type Abo struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (a Abo) pruefen() error {
	if !strings.HasPrefix(a.Endpoint, "https://") || len(a.Endpoint) > 2048 || a.Keys.P256dh == "" || a.Keys.Auth == "" ||
		len(a.Keys.P256dh) > 200 || len(a.Keys.Auth) > 100 {
		return ErrUngueltig
	}
	return nil
}

// Store speichert Abos; die Tabelle ist global, der Zugriff läuft je Konto.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore erzeugt einen Store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Speichern legt das Abo für das Konto an. Meldet sich am Gerät ein anderes Konto an, wandert es mit.
func (s *Store) Speichern(ctx context.Context, kontoID string, a Abo) error {
	if err := a.pruefen(); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO push_abo (endpoint, konto_id, p256dh, auth) VALUES ($1, $2, $3, $4)
ON CONFLICT (endpoint) DO UPDATE SET konto_id = EXCLUDED.konto_id, p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth`,
		a.Endpoint, kontoID, a.Keys.P256dh, a.Keys.Auth)
	if err != nil {
		return fmt.Errorf("push-abo speichern: %w", err)
	}
	return nil
}

// Loeschen entfernt ein Abo des Kontos (Abmelden am Gerät).
func (s *Store) Loeschen(ctx context.Context, kontoID, endpoint string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM push_abo WHERE konto_id = $1 AND endpoint = $2`, kontoID, endpoint)
	return err
}

// Entfernen löscht ein abgelaufenes Abo, egal welchem Konto es gehört.
func (s *Store) Entfernen(ctx context.Context, endpoint string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM push_abo WHERE endpoint = $1`, endpoint)
	return err
}

// Abos liefert alle Abos des Kontos.
func (s *Store) Abos(ctx context.Context, kontoID string) ([]Abo, error) {
	rows, err := s.pool.Query(ctx, `SELECT endpoint, p256dh, auth FROM push_abo WHERE konto_id = $1 ORDER BY created_at`, kontoID)
	if err != nil {
		return nil, fmt.Errorf("push-abos laden: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Abo, error) {
		var a Abo
		return a, r.Scan(&a.Endpoint, &a.Keys.P256dh, &a.Keys.Auth)
	})
}

// Sender verschickt verschlüsselte Push-Nachrichten.
type Sender struct {
	PublicKey, PrivateKey string
	// Subject identifiziert den Absender gegenüber dem Push-Dienst, z. B. mailto:teamtafel@schwarzpost.de.
	Subject string
	Client  *http.Client
}

// Senden stellt data an ein Abo zu. Antwortet der Dienst mit 404 oder 410, ist das Ergebnis ErrAbgelaufen.
func (s *Sender) Senden(ctx context.Context, a Abo, data []byte) error {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := webpush.SendNotificationWithContext(ctx, data, &webpush.Subscription{
		Endpoint: a.Endpoint,
		Keys:     webpush.Keys{P256dh: a.Keys.P256dh, Auth: a.Keys.Auth},
	}, &webpush.Options{
		HTTPClient:      client,
		Subscriber:      s.Subject,
		VAPIDPublicKey:  s.PublicKey,
		VAPIDPrivateKey: s.PrivateKey,
		TTL:             24 * 60 * 60,
		Urgency:         webpush.UrgencyNormal,
	})
	if err != nil {
		return fmt.Errorf("push senden: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrAbgelaufen
	case resp.StatusCode >= 300:
		return fmt.Errorf("push-dienst antwortet %d", resp.StatusCode)
	}
	return nil
}
