// Package kalender liefert die Termine eines Kontos als iCal-Abo (RFC 5545).
package kalender

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruben-sch/teamtafel/internal/db"
	"github.com/ruben-sch/teamtafel/internal/termin"
)

// ErrUnbekannt: kein gültiger Kalender-Link.
var ErrUnbekannt = errors.New("kalender-link unbekannt")

// Store verwaltet die geheimen Kalender-Links; gespeichert wird nur der Hash.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore erzeugt einen Store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Erneuern erzeugt einen neuen Link für Konto und Verein; ein bisheriger wird ungültig.
// Das Token gibt es nur hier im Klartext.
func (s *Store) Erneuern(ctx context.Context, vereinID, kontoID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	err := db.InVerein(ctx, s.pool, vereinID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
INSERT INTO kalender_token (verein_id, konto_id, token_hash) VALUES ($1, $2, $3)
ON CONFLICT (verein_id, konto_id) DO UPDATE SET token_hash = EXCLUDED.token_hash, created_at = now()`,
			vereinID, kontoID, hash(token))
		return err
	})
	if err != nil {
		return "", fmt.Errorf("kalender-link erneuern: %w", err)
	}
	return token, nil
}

// Vorhanden meldet, ob das Konto im Verein einen Kalender-Link hat.
func (s *Store) Vorhanden(ctx context.Context, vereinID, kontoID string) (bool, error) {
	var ok bool
	err := db.InVerein(ctx, s.pool, vereinID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM kalender_token WHERE konto_id = $1)`, kontoID).Scan(&ok)
	})
	return ok, err
}

// Konto liefert das Konto zu einem Kalender-Link im Verein.
func (s *Store) Konto(ctx context.Context, vereinID, token string) (string, error) {
	var kontoID string
	err := db.InVerein(ctx, s.pool, vereinID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT konto_id::text FROM kalender_token WHERE token_hash = $1`, hash(token)).Scan(&kontoID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnbekannt
	}
	return kontoID, err
}

func hash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Kopf beschreibt den Kalender.
type Kopf struct {
	Name   string // Anzeigename, z. B. der Verein
	Host   string // Vereins-Host für UID und Links
	Scheme string
}

// ICS schreibt die Termine als VCALENDAR. Abgesagte Termine bleiben mit STATUS:CANCELLED drin,
// damit sie in abonnierten Kalendern als ausgefallen erscheinen statt kommentarlos zu verschwinden.
func ICS(w io.Writer, k Kopf, ts []termin.Termin, jetzt time.Time) {
	z := &zeilen{w: w}
	z.add("BEGIN:VCALENDAR")
	z.add("VERSION:2.0")
	z.add("PRODID:-//Teamtafel//Termine//DE")
	z.add("CALSCALE:GREGORIAN")
	z.add("METHOD:PUBLISH")
	z.add("X-WR-CALNAME:" + text(k.Name))
	z.add("X-WR-TIMEZONE:Europe/Berlin")
	z.add("REFRESH-INTERVAL;VALUE=DURATION:PT1H")
	z.add("X-PUBLISHED-TTL:PT1H")
	for _, t := range ts {
		titel := t.Mannschaft + ": " + termin.TypName(t.Typ)
		if t.Titel != "" {
			titel += " " + t.Titel
		}
		if t.Abgesagt {
			titel = "Abgesagt: " + titel
		}
		var beschreibung []string
		if t.Treffzeit != nil {
			s := "Treffen " + t.Treffzeit.In(termin.Zeitzone).Format("15:04") + " Uhr"
			if t.Treffpunkt != "" {
				s += ", " + t.Treffpunkt
			}
			beschreibung = append(beschreibung, s)
		}
		link := k.Scheme + "://" + k.Host + "/t/" + t.ID
		beschreibung = append(beschreibung, "In Teamtafel: "+link)

		z.add("BEGIN:VEVENT")
		z.add("UID:" + t.ID + "@" + k.Host)
		z.add("DTSTAMP:" + utc(jetzt))
		z.add("DTSTART:" + utc(t.Beginn))
		z.add("DTEND:" + utc(t.Ende))
		z.add("SUMMARY:" + text(titel))
		if t.Ort != "" {
			z.add("LOCATION:" + text(t.Ort))
		}
		z.add("DESCRIPTION:" + text(strings.Join(beschreibung, "\n")))
		z.add("URL:" + link)
		if t.Abgesagt {
			z.add("STATUS:CANCELLED")
		} else {
			z.add("STATUS:CONFIRMED")
		}
		z.add("END:VEVENT")
	}
	z.add("END:VCALENDAR")
}

func utc(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

// text maskiert einen TEXT-Wert nach RFC 5545 3.3.11.
func text(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`).Replace(s)
}

// zeilen schreibt Content-Lines mit CRLF und faltet nach 75 Oktetten, ohne UTF-8-Zeichen zu teilen.
type zeilen struct{ w io.Writer }

func (z *zeilen) add(line string) {
	var sb strings.Builder
	max := 75
	for len(line) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		sb.WriteString(line[:cut])
		sb.WriteString("\r\n ")
		line = line[cut:]
		max = 74 // Folgezeilen beginnen mit einem Leerzeichen
	}
	sb.WriteString(line)
	sb.WriteString("\r\n")
	_, _ = io.WriteString(z.w, sb.String())
}
