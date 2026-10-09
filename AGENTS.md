# AGENTS.md

Leitfaden für Coding-Agents. Projektsprache ist Deutsch: Doku, Kommentare und UI-Texte auf Deutsch, Bezeichner im Code englisch.

## Überblick
Teamtafel ist die Termin- und Teamverwaltung für Jugendfußball-Vereine (mandantenfähig, ein Verein = ein Mandant). Eltern und Spieler sagen zu Trainings und Spielen zu oder ab, Trainer legen Termine an und sehen Rückmeldungen.

Stack: Go 1.26 (net/http, html/template), PostgreSQL 17 (pgx/v5), Migrationen mit goose (eingebettet), Distroless-Image, Docker Compose hinter einem gemeinsamen Traefik.

## Struktur
- `cmd/teamtafel/` – Einstieg: migriert, startet den Server; Subcommand `healthcheck` für Docker.
- `internal/config/` – Konfiguration ausschließlich aus Umgebungsvariablen.
- `internal/db/` – Pool und Migrationen.
- `internal/verein/` – Mandanten (Verein), Saisons, Mannschaften.
- `internal/auth/` – Magic-Link-Login, Sessions, Rate-Limit.
- `internal/team/` – Trainer, Spieler, Vertretungen (Eltern), Kader, Team-Links und Beitrittsanfragen.
- `internal/termin/` – Termine, wöchentliche Serien und Rückmeldungen (Zu/Ab); die Wartung in `main.go` erzeugt stündlich Serientermine 8 Wochen im Voraus und erinnert alle 5 Minuten vor Fristen. Zeiten in Europe/Berlin, gespeichert in UTC.
- `internal/job/` – Outbox und Job-Queue (`job`-Tabelle, global ohne RLS): `Einreihen` in der fachlichen Transaktion, Worker mit `FOR UPDATE SKIP LOCKED` und Backoff.
- `internal/nachricht/` – Benachrichtigungen: Empfänger (`Team`, `Trainer`, `Spieler`), `An` reiht je Konto einen Job ein, `Zustellung` verschickt per Web Push, sonst Mail.
- `internal/push/` – Web-Push-Abos (`push_abo`, global je Konto) und Versand per VAPID (webpush-go); 404/410 heißt Abo löschen. Texte entstehen in den Fachpaketen; nur Vornamen, keine Absagegründe.
- `internal/mail/` – SMTP-Versand (lokal Mailpit, sonst Resend); Tests laufen gegen Mailpit (`TEST_SMTP_ADDR`, `TEST_MAILPIT_URL`).
- `internal/web/` – Handler, Templates und `static/` (Service Worker, Manifest, Icons, `app.js`); `mandant.go` löst den Verein aus der Subdomain auf.
- `internal/dbtest/` – Integrationstests: migrierte DB und Pool mit App-Rolle ohne BYPASSRLS.
- `migrations/` – SQL-Migrationen (`NNNNN_name.sql`), per `embed` im Binary.
- `deploy/` – Compose-Dateien für Staging/Produktion und das Init-Skript der App-Rolle.

## Arbeitsweise
- Testgetrieben: erst der fehlschlagende Test, dann der Code.
- `make test` startet die lokale Datenbank und führt alle Tests aus. Integrationstests brauchen `TEST_DATABASE_URL`; ohne sie werden sie lokal übersprungen, in CI sind sie Pflicht.
- `make lint` vor jedem Commit.
- Schemaänderungen nur als neue Migration, bestehende Migrationen nie ändern.

## Sicherheit
- Die App verbindet sich als `teamtafel_app` (ohne BYPASSRLS), Migrationen laufen als Owner `teamtafel`. Mandantentrennung per Row-Level-Security.
- Jede Tabelle mit `verein_id` braucht RLS und die Policy `verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid` (USING und WITH CHECK). `NULLIF` ist nötig, weil der Wert auf wiederverwendeten Pool-Verbindungen nach einer Transaktion `''` statt NULL ist. `TestJedeVereinstabelleHatRLS` prüft das.
- Vereinsdaten nur über `db.InVerein` lesen und schreiben; Integrationstests nutzen `dbtest.AppPool`, nie den Superuser, sonst greift RLS nicht.
- Keine Secrets im Repo. Passwörter kommen aus GitHub-Environment-Secrets.

## Git
- Conventional Commits (`feat:`, `fix:`, `chore:` …), Branches `feat/…`, `fix/…`.
- PRs werden squash-gemergt; der PR-Titel steuert release-please.
- Push auf `main` deployt Staging, ein Release deployt Produktion.
