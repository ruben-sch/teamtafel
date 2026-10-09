-- +goose Up
-- Mandant. Weitere vereinsbezogene Tabellen tragen verein_id und Row-Level-Security.
CREATE TABLE verein (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       text NOT NULL UNIQUE,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE verein;
