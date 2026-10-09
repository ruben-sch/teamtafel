-- +goose Up
-- Global, ohne verein_id: Ein Konto kann in mehreren Vereinen aktiv sein.
CREATE TABLE konto (
    id              uuid PRIMARY KEY DEFAULT gen_uuid_v7(),
    email           text NOT NULL UNIQUE CHECK (email = lower(email)),
    name            text NOT NULL DEFAULT '',
    plattform_admin boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- Magic-Link. Das Konto entsteht erst beim Einlösen, daher die Adresse statt konto_id.
CREATE TABLE login_token (
    token_hash   bytea PRIMARY KEY,
    email        text NOT NULL,
    ablauf       timestamptz NOT NULL,
    verwendet_am timestamptz
);
CREATE INDEX login_token_ablauf ON login_token (ablauf);

CREATE TABLE session (
    token_hash bytea PRIMARY KEY,
    konto_id   uuid NOT NULL REFERENCES konto (id) ON DELETE CASCADE,
    ablauf     timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX session_konto ON session (konto_id);

-- +goose Down
DROP TABLE session;
DROP TABLE login_token;
DROP TABLE konto;
