-- +goose Up
-- Vereinsadmins verwalten Mannschaften, Trainer und weitere Admins ihres Vereins.
-- Super-Admins der Plattform kommen aus der Konfiguration (SUPERADMIN_EMAILS).
CREATE TABLE verein_admin (
    verein_id  uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    konto_id   uuid NOT NULL REFERENCES konto (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (verein_id, konto_id)
);

ALTER TABLE verein_admin ENABLE ROW LEVEL SECURITY;
CREATE POLICY verein_isolation ON verein_admin
    USING (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid)
    WITH CHECK (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid);

-- +goose Down
DROP TABLE verein_admin;
