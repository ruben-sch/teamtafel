-- +goose Up
-- iCal-Abo: ein geheimer Link je Konto und Verein. Gespeichert wird nur der Hash.
CREATE TABLE kalender_token (
    verein_id  uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    konto_id   uuid NOT NULL REFERENCES konto (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (verein_id, konto_id)
);

ALTER TABLE kalender_token ENABLE ROW LEVEL SECURITY;
CREATE POLICY verein_isolation ON kalender_token
    USING (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid)
    WITH CHECK (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid);

-- +goose Down
DROP TABLE kalender_token;
