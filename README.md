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
- **Eltern und Spieler** sagen direkt auf der Startseite zu oder ab, im Termin auch mit Grund (krank, Urlaub, sonstiges). Bis zur Frist (ohne Frist bis zum Beginn) geht das selbst, danach nur über den Trainer. Für Kinder antworten die Eltern, solange das Kind minderjährig ist. Trainer sehen Zähler und Gründe.
- **Trainer** öffnen ihre Mannschaft und erzeugen dort den Team-Link mit QR-Code. Eltern (oder Spieler selbst) melden sich über den Link an und stellen eine Beitrittsanfrage, die der Trainer freigibt oder ablehnt.
- **Kalender-Abo:** Unter „Einstellungen“ erzeugt jedes Konto je Verein einen geheimen iCal-Link (`/kalender/<token>.ics`) mit den Terminen seiner Mannschaften, vier Wochen zurück bis ein Jahr voraus. Abgesagte Termine bleiben als „Abgesagt“ drin. Gespeichert wird nur der Hash; ein neuer Link macht den alten ungültig.

### Benachrichtigungen

Per Web Push an alle Geräte, die ein Konto unter „Einstellungen“ freigeschaltet hat, sonst per E-Mail; Terminabsagen kommen immer auch per E-Mail. Push braucht ein VAPID-Schlüsselpaar je Environment (Secret `VAPID_PRIVATE_KEY`, Variable `VAPID_PUBLIC_KEY`); ohne bleibt es bei E-Mail. Auf dem iPhone geht Push nur, wenn Teamtafel auf dem Home-Bildschirm liegt. Jede Benachrichtigung entsteht als Job in derselben Transaktion wie die Änderung; ein Worker im App-Prozess verschickt sie und versucht es bei Fehlern bis zu acht Mal mit wachsendem Abstand.

| Ereignis | Empfänger |
|---|---|
| Neuer Einzeltermin, Zeit oder Ort geändert, Termin abgesagt | Mannschaft (Trainer, Spieler mit Login, Eltern) außer dem Auslöser |
| 24 Stunden vor der Frist (ohne Frist: vor Beginn) | wer für noch offene Spieler antworten darf |
| Zu- oder Absage geändert | andere Konten desselben Spielers; nach der Frist auch die Trainer |
| Neue Beitrittsanfrage | Trainer der Mannschaft |

Neue Serientermine und vergangene Termine lösen nichts aus. Inhalte nennen nur Vornamen und nie den Absagegrund. Auf Staging begrenzt die Environment-Variable `MAIL_ALLOWLIST` (Adressen durch Komma getrennt) Push und E-Mail, damit keine echten Eltern Post bekommen; Login-Mails sind davon ausgenommen.

### Löschfristen

Die Wartung in der App setzt sie stündlich um:
- Absagegründe werden 90 Tage nach Terminbeginn gelöscht, die Absage selbst bleibt.
- Abgelaufene Login-Links und Sessions werden gelöscht, erledigte Benachrichtigungs-Jobs nach 30 Tagen.
- Abgelehnte Beitrittsanfragen werden sofort gelöscht.
- Spieler, die seit 6 Monaten in keinem Kader einer laufenden Saison stehen, werden mit Rückmeldungen und Vertretungen gelöscht, freigegebene Anfragen abgelaufener Saisons ebenso.
- Konten, die in keinem Verein mehr Trainer, Admin, Spieler oder Vertretung sind, werden nach 30 Tagen gelöscht. 7 Tage vorher geht ein Hinweis per Mail; ohne verschickten Hinweis (etwa auf Staging außerhalb von `MAIL_ALLOWLIST`) wird nicht gelöscht. Super-Admins sind ausgenommen.
- Logs und gespeicherte Fehlermeldungen enthalten nur IDs, keine E-Mail-Adressen oder Namen.

Damit der Verein erreichbar ist, kommt sein Slug in die Environment-Variable `VEREIN_SLUGS` (Settings → Environments → Variables, mehrere durch Leerzeichen getrennt). Der nächste Deploy trägt `<slug>.<APP_HOST>` in Traefik ein; Traefik holt das Zertifikat per HTTP-Challenge. DNS: Der Anbieter (domaindiscount24) kann keine Wildcards, daher je Verein zwei A-Records auf die VM: `<slug>.teamtafel.schwarzpost.de` und `<slug>.staging.teamtafel.schwarzpost.de`.

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
