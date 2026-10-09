#!/usr/bin/env bash
# Richtet GitHub für das Deployment ein: Environments, Secrets, Actions-Rechte.
# Passwörter werden einmal erzeugt und in $SECRETS_FILE gespeichert. Beim
# erneuten Lauf werden sie wiederverwendet, denn die Datenbank übernimmt sie
# nur beim ersten Start.
#
# Aufruf: scripts/setup-github.sh <ssh-private-key> [host] [user]
set -euo pipefail

REPO="${REPO:-ruben-sch/teamtafel}"
DOMAIN="${DOMAIN:-teamtafel.schwarzpost.de}"
SECRETS_FILE="${SECRETS_FILE:-.secrets/github.env}"

KEY_FILE="${1:?Pfad zum privaten SSH-Key angeben}"
HOST="${2:-135.181.30.178}"
USER_NAME="${3:-root}"

command -v gh >/dev/null || { echo "gh fehlt" >&2; exit 1; }
gh auth status >/dev/null
[ -r "$KEY_FILE" ] || { echo "Key $KEY_FILE nicht lesbar" >&2; exit 1; }

mkdir -p "$(dirname "$SECRETS_FILE")"
chmod 700 "$(dirname "$SECRETS_FILE")"
touch "$SECRETS_FILE"
chmod 600 "$SECRETS_FILE"

# Liefert den gespeicherten Wert oder erzeugt und speichert einen neuen.
pw() {
  local name="$1" val
  val="$(grep -E "^${name}=" "$SECRETS_FILE" | cut -d= -f2- || true)"
  if [ -z "$val" ]; then
    val="$(openssl rand -hex 24)"
    echo "${name}=${val}" >> "$SECRETS_FILE"
  fi
  printf '%s' "$val"
}

echo "Repo-Secrets für $REPO"
gh secret set HETZNER_SSH_KEY -R "$REPO" < "$KEY_FILE"
gh secret set HETZNER_HOST -R "$REPO" -b "$HOST"
gh secret set HETZNER_USER -R "$REPO" -b "$USER_NAME"

for env in staging production; do
  if [ "$env" = production ]; then host="$DOMAIN"; else host="staging.$DOMAIN"; fi
  prefix="$(echo "$env" | tr '[:lower:]' '[:upper:]')"
  echo "Environment $env ($host)"
  gh api -X PUT "repos/$REPO/environments/$env" --silent
  gh secret set APP_HOST -R "$REPO" -e "$env" -b "$host"
  gh secret set POSTGRES_PASSWORD -R "$REPO" -e "$env" -b "$(pw "${prefix}_POSTGRES_PASSWORD")"
  gh secret set APP_DB_PASSWORD -R "$REPO" -e "$env" -b "$(pw "${prefix}_APP_DB_PASSWORD")"
done

echo "Actions dürfen PRs anlegen (release-please)"
gh api -X PUT "repos/$REPO/actions/permissions/workflow" \
  -f default_workflow_permissions=read -F can_approve_pull_request_reviews=true --silent

echo "Fertig. Passwörter liegen in $SECRETS_FILE (nicht committen, sicher aufbewahren)."
