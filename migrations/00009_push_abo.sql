-- +goose Up
-- Web-Push-Abo eines Geräts. Global ohne verein_id: Es gehört zum Konto, nicht zum Verein.
CREATE TABLE push_abo (
    endpoint   text PRIMARY KEY,
    konto_id   uuid NOT NULL REFERENCES konto (id) ON DELETE CASCADE,
    p256dh     text NOT NULL,
    auth       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX push_abo_konto ON push_abo (konto_id);

-- +goose Down
DROP TABLE push_abo;
