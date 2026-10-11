-- +goose Up
-- Dienst an einem Termin (Kuchen, Kasse, Trikotwäsche) mit einer festen Zahl an Plätzen.
CREATE TABLE dienst (
    id          uuid PRIMARY KEY DEFAULT gen_uuid_v7(),
    verein_id   uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    termin_id   uuid NOT NULL,
    bezeichnung text NOT NULL CHECK (bezeichnung <> '' AND length(bezeichnung) <= 80),
    plaetze     integer NOT NULL CHECK (plaetze BETWEEN 1 AND 20),
    created_at  timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (verein_id, termin_id) REFERENCES termin (verein_id, id) ON DELETE CASCADE,
    UNIQUE (verein_id, id)
);
CREATE INDEX dienst_termin ON dienst (termin_id);

-- Eintrag eines Kontos in einen Dienst; ein Konto belegt höchstens einen Platz.
-- eingeteilt_von ist gesetzt, wenn ein Trainer das Konto eingetragen hat.
CREATE TABLE dienst_eintrag (
    verein_id      uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    dienst_id      uuid NOT NULL,
    konto_id       uuid NOT NULL REFERENCES konto (id) ON DELETE CASCADE,
    eingeteilt_von uuid REFERENCES konto (id) ON DELETE SET NULL,
    -- Erinnerung am Vortag verschickt (oder beim Eintragen schon zu spät dafür).
    erinnert_am    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (dienst_id, konto_id),
    FOREIGN KEY (verein_id, dienst_id) REFERENCES dienst (verein_id, id) ON DELETE CASCADE
);
CREATE INDEX dienst_eintrag_konto ON dienst_eintrag (konto_id);

-- Hinweis „Plätze frei“ an die Mannschaft, höchstens einmal je Termin.
ALTER TABLE termin ADD COLUMN dienst_hinweis_am timestamptz;

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['dienst', 'dienst_eintrag'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format($p$CREATE POLICY verein_isolation ON %I
      USING (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid)
      WITH CHECK (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid)$p$, t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE termin DROP COLUMN dienst_hinweis_am;
DROP TABLE dienst_eintrag;
DROP TABLE dienst;
