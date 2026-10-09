# AGENTS.md

Leitfaden für Coding-Agents. Projektsprache ist Deutsch: Doku, Kommentare und UI-Texte auf Deutsch, Bezeichner im Code englisch.

## Überblick
Teamtafel ist die Termin- und Teamverwaltung für Jugendfußball-Vereine (mandantenfähig, ein Verein = ein Mandant). Eltern und Spieler sagen zu Trainings und Spielen zu oder ab, Trainer legen Termine an und sehen Rückmeldungen.

Stack: Go 1.26 (net/http, html/template), PostgreSQL 17 (pgx/v5), Migrationen mit goose (eingebettet), Distroless-Image, Docker Compose hinter einem gemeinsamen Traefik.

## Struktur
- `cmd/teamtafel/` – Einstieg: migriert, startet den Server; Subcommand `healthcheck` für Docker.
- `internal/config/` – Konfiguration ausschließlich aus Umgebungsvariablen.
- `internal/db/` – Pool und Migrationen.
- `internal/web/` – Handler und Templates.
- `migrations/` – SQL-Migrationen (`NNNNN_name.sql`), per `embed` im Binary.
- `deploy/` – Compose-Dateien für Staging/Produktion und das Init-Skript der App-Rolle.

## Arbeitsweise
- Testgetrieben: erst der fehlschlagende Test, dann der Code.
- `make test` startet die lokale Datenbank und führt alle Tests aus. Integrationstests brauchen `TEST_DATABASE_URL`; ohne sie werden sie lokal übersprungen, in CI sind sie Pflicht.
- `make lint` vor jedem Commit.
- Schemaänderungen nur als neue Migration, bestehende Migrationen nie ändern.

## Sicherheit
- Die App verbindet sich als `teamtafel_app` (ohne BYPASSRLS), Migrationen laufen als Owner `teamtafel`. Mandantentrennung per Row-Level-Security.
- Keine Secrets im Repo. Passwörter kommen aus GitHub-Environment-Secrets.

## Git
- Conventional Commits (`feat:`, `fix:`, `chore:` …), Branches `feat/…`, `fix/…`.
- PRs werden squash-gemergt; der PR-Titel steuert release-please.
- Push auf `main` deployt Staging, ein Release deployt Produktion.
