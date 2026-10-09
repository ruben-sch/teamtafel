-- +goose Up
CREATE TABLE trainer (
    verein_id     uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    mannschaft_id uuid NOT NULL REFERENCES mannschaft (id) ON DELETE CASCADE,
    konto_id      uuid NOT NULL REFERENCES konto (id) ON DELETE CASCADE,
    PRIMARY KEY (mannschaft_id, konto_id)
);
CREATE INDEX trainer_konto ON trainer (konto_id);

CREATE TABLE spieler (
    id         uuid PRIMARY KEY DEFAULT gen_uuid_v7(),
    verein_id  uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    vorname    text NOT NULL,
    nachname   text NOT NULL,
    jahrgang   int  NOT NULL CHECK (jahrgang BETWEEN 1900 AND 2100),
    -- Eigener Login (ältere Kinder, Erwachsene). Höchstens ein Spieler je Konto und Verein.
    konto_id   uuid REFERENCES konto (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (verein_id, konto_id)
);
CREATE INDEX spieler_name ON spieler (verein_id, lower(nachname), lower(vorname), jahrgang);

CREATE TABLE vertretung (
    verein_id  uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    spieler_id uuid NOT NULL REFERENCES spieler (id) ON DELETE CASCADE,
    konto_id   uuid NOT NULL REFERENCES konto (id) ON DELETE CASCADE,
    PRIMARY KEY (spieler_id, konto_id)
);
CREATE INDEX vertretung_konto ON vertretung (konto_id);

CREATE TABLE kader (
    verein_id     uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    mannschaft_id uuid NOT NULL REFERENCES mannschaft (id) ON DELETE CASCADE,
    spieler_id    uuid NOT NULL REFERENCES spieler (id) ON DELETE CASCADE,
    PRIMARY KEY (mannschaft_id, spieler_id)
);

-- Team-Link. Gespeichert wird nur der Hash; erneuern deaktiviert den alten Link.
CREATE TABLE einladung (
    id            uuid PRIMARY KEY DEFAULT gen_uuid_v7(),
    verein_id     uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    mannschaft_id uuid NOT NULL REFERENCES mannschaft (id) ON DELETE CASCADE,
    token_hash    bytea NOT NULL UNIQUE,
    aktiv         boolean NOT NULL DEFAULT true,
    gueltig_bis   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX einladung_eine_aktive ON einladung (mannschaft_id) WHERE aktiv;

CREATE TABLE beitrittsanfrage (
    id                 uuid PRIMARY KEY DEFAULT gen_uuid_v7(),
    verein_id          uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    mannschaft_id      uuid NOT NULL REFERENCES mannschaft (id) ON DELETE CASCADE,
    konto_id           uuid NOT NULL REFERENCES konto (id) ON DELETE CASCADE,
    art                text NOT NULL CHECK (art IN ('kind', 'selbst')),
    vorname            text NOT NULL,
    nachname           text NOT NULL,
    jahrgang           int  NOT NULL CHECK (jahrgang BETWEEN 1900 AND 2100),
    treffer_spieler_id uuid REFERENCES spieler (id) ON DELETE SET NULL,
    status             text NOT NULL DEFAULT 'offen' CHECK (status IN ('offen', 'freigegeben')),
    entschieden_von    uuid REFERENCES konto (id) ON DELETE SET NULL,
    entschieden_am     timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now()
);
-- Dieselbe Person kann dasselbe Kind nicht doppelt offen anfragen.
CREATE UNIQUE INDEX beitrittsanfrage_offen ON beitrittsanfrage
    (mannschaft_id, konto_id, lower(vorname), lower(nachname), jahrgang) WHERE status = 'offen';

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['trainer', 'spieler', 'vertretung', 'kader', 'einladung', 'beitrittsanfrage'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format($p$CREATE POLICY verein_isolation ON %I
      USING (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid)
      WITH CHECK (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid)$p$, t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE beitrittsanfrage;
DROP TABLE einladung;
DROP TABLE kader;
DROP TABLE vertretung;
DROP TABLE spieler;
DROP TABLE trainer;
