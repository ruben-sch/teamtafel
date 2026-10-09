# Teamtafel

Termin- und Teamverwaltung für Jugendfußball: Trainer legen Trainings und Spiele an, Spieler bzw. Eltern sagen zu oder ab. Mandantenfähig für mehrere Vereine.

## Lokal starten

Voraussetzungen: Docker, Go 1.26.

```sh
make up      # App auf http://localhost:8080, Mailpit (Login-Mails) auf http://localhost:8025
make test    # alle Tests inkl. Integration gegen Postgres
make lint
```

## Verwaltung

Jeder Verein ist ein Mandant und läuft unter `<slug>.<APP_HOST>`.

- **Plattform-Admins** stehen in der Environment-Variable `SUPERADMIN_EMAILS` (Settings → Environments → Variables, mehrere durch Komma getrennt). Sie melden sich auf der Hauptdomain an und legen unter `/admin` Vereine samt erstem Vereinsadmin an. Lokal ist das `admin@example.org`.
- **Vereinsadmins** melden sich auf der Vereins-Subdomain an und legen unter `/admin` Mannschaften an, tragen Trainer ein oder entfernen sie und ernennen weitere Vereinsadmins.
- **Trainer** (und Vereinsadmins) legen auf der Mannschaftsseite Einzeltermine und wöchentliche Trainingsserien an, ändern Termine oder sagen sie ab. Serientermine entstehen jeweils acht Wochen im Voraus.
- **Trainer** öffnen ihre Mannschaft und erzeugen dort den Team-Link mit QR-Code. Eltern (oder Spieler selbst) melden sich über den Link an und stellen eine Beitrittsanfrage, die der Trainer freigibt oder ablehnt.

Damit der Verein erreichbar ist, kommt sein Slug in die Environment-Variable `VEREIN_SLUGS` (Settings → Environments → Variables, mehrere durch Leerzeichen getrennt). Der nächste Deploy trägt `<slug>.<APP_HOST>` in Traefik ein; Traefik holt das Zertifikat per HTTP-Challenge. DNS: Wildcard-Einträge `*.teamtafel.schwarzpost.de` und `*.staging.teamtafel.schwarzpost.de` auf die VM.

Lokal: `http://<slug>.localhost:8080` (Browser lösen `*.localhost` selbst auf).

## Deployment

| Auslöser | Umgebung | Image-Tag | Compose-Projekt |
|---|---|---|---|
| Push auf `main` | `staging` | `staging` | `teamtafel-staging` |
| Release (release-please) | `production` | `latest`, `X.Y.Z` | `teamtafel` |

Der Workflow baut das Image nach GHCR, kopiert `deploy/` per SCP auf den Server und startet den Stack per SSH mit `docker compose up -d --wait`. Auf dem Server wird der gemeinsame Traefik im externen Docker-Netz `proxy` vorausgesetzt (Entrypoint `websecure`, Resolver `letsencrypt`, für Staging die BasicAuth-Middleware `auth@docker`).

### Einmalige Einrichtung

Schritte 2 bis 4 erledigt `scripts/setup-github.sh <ssh-private-key> [host] [user]` (braucht `gh` und `openssl`). Die erzeugten Passwörter landen in `.secrets/github.env` und werden bei erneutem Lauf wiederverwendet.

1. **DNS**: A-/AAAA-Einträge für `teamtafel.schwarzpost.de` und `staging.teamtafel.schwarzpost.de` auf die VM.
2. **Repo-Secrets** (Settings → Secrets and variables → Actions): `HETZNER_HOST`, `HETZNER_USER`, `HETZNER_SSH_KEY`.
3. **Environments** `staging` und `production` anlegen, je mit den Secrets:
   - `APP_HOST` – z. B. `staging.teamtafel.schwarzpost.de`
   - `POSTGRES_PASSWORD`, `APP_DB_PASSWORD` – nur Hex-Zeichen, z. B. `openssl rand -hex 24`
   - `SMTP_PASSWORD` – Resend-API-Key für Login-Mails (Versand per SMTP über `smtp.resend.com:587`, Absender `teamtafel@schwarzpost.de`; `schwarzpost.de` ist in Resend verifiziert, Subdomains bräuchten eine eigene Verifizierung). Ohne den Key startet die App, Login-Mails schlagen fehl.
4. **release-please**: Settings → Actions → General → „Allow GitHub Actions to create and approve pull requests“ aktivieren.
5. **Claude-Review**: Repo-Secret `CLAUDE_CODE_OAUTH_TOKEN` setzen (Token per `claude setup-token`, dann `gh secret set CLAUDE_CODE_OAUTH_TOKEN -R ruben-sch/teamtafel`).
6. Nach dem ersten Push das GHCR-Paket prüfen (Sichtbarkeit privat genügt, der Deploy loggt sich mit dem Workflow-Token ein).

Hinweis: Die App-Rolle `teamtafel_app` wird nur beim ersten Start der Datenbank angelegt. Ein späterer Wechsel von `APP_DB_PASSWORD` muss per `ALTER ROLE` nachgezogen werden.
