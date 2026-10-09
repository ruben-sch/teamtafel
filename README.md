# Teamtafel

Termin- und Teamverwaltung für Jugendfußball: Trainer legen Trainings und Spiele an, Spieler bzw. Eltern sagen zu oder ab. Mandantenfähig für mehrere Vereine.

## Lokal starten

Voraussetzungen: Docker, Go 1.26.

```sh
make up      # App auf http://localhost:8080, Mailpit auf http://localhost:8025
make test    # alle Tests inkl. Integration gegen Postgres
make lint
```

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
4. **release-please**: Settings → Actions → General → „Allow GitHub Actions to create and approve pull requests“ aktivieren.
5. **Claude-Review**: Repo-Secret `CLAUDE_CODE_OAUTH_TOKEN` setzen (Token per `claude setup-token`, dann `gh secret set CLAUDE_CODE_OAUTH_TOKEN -R ruben-sch/teamtafel`).
6. Nach dem ersten Push das GHCR-Paket prüfen (Sichtbarkeit privat genügt, der Deploy loggt sich mit dem Workflow-Token ein).

Hinweis: Die App-Rolle `teamtafel_app` wird nur beim ersten Start der Datenbank angelegt. Ein späterer Wechsel von `APP_DB_PASSWORD` muss per `ALTER ROLE` nachgezogen werden.
