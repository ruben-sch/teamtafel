#!/bin/sh
# Legt die App-Rolle ohne BYPASSRLS an. Der Postgres-Container führt das
# Skript einmalig beim ersten Start mit leerem Datenverzeichnis aus.
# Tabellen gehören der Owner-Rolle (POSTGRES_USER), die App bekommt nur DML-Rechte.
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v app_password="$APP_DB_PASSWORD" -v owner="$POSTGRES_USER" -v db="$POSTGRES_DB" <<'SQL'
CREATE ROLE teamtafel_app LOGIN PASSWORD :'app_password' NOSUPERUSER NOBYPASSRLS;
GRANT CONNECT ON DATABASE :"db" TO teamtafel_app;
GRANT USAGE ON SCHEMA public TO teamtafel_app;
ALTER DEFAULT PRIVILEGES FOR ROLE :"owner" IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO teamtafel_app;
ALTER DEFAULT PRIVILEGES FOR ROLE :"owner" IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO teamtafel_app;
SQL
