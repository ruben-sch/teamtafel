-- +goose Up
-- Für zusammengesetzte Fremdschlüssel: Termine gehören zu einer Mannschaft desselben Vereins.
ALTER TABLE mannschaft ADD CONSTRAINT mannschaft_verein_id_id UNIQUE (verein_id, id);

-- Vorlage für ein wöchentliches Training. Ein Job erzeugt daraus echte Termine
-- für die nächsten 8 Wochen; Uhrzeiten gelten in Europe/Berlin.
CREATE TABLE terminserie (
    id               uuid PRIMARY KEY DEFAULT gen_uuid_v7(),
    verein_id        uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    mannschaft_id    uuid NOT NULL,
    wochentag        smallint NOT NULL CHECK (wochentag BETWEEN 1 AND 7), -- 1 = Montag
    uhrzeit          time NOT NULL,
    dauer_min        integer NOT NULL CHECK (dauer_min > 0),
    -- Treffzeit und Rückmeldefrist in Minuten vor Beginn; 0 heißt keine.
    treff_min        integer NOT NULL DEFAULT 0 CHECK (treff_min >= 0),
    frist_min        integer NOT NULL DEFAULT 0 CHECK (frist_min >= 0),
    ort              text NOT NULL DEFAULT '',
    gueltig_von      date NOT NULL,
    gueltig_bis      date,
    -- Beendete Serien erzeugen keine Termine mehr.
    beendet_am       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (verein_id, mannschaft_id) REFERENCES mannschaft (verein_id, id) ON DELETE CASCADE,
    UNIQUE (verein_id, id),
    CHECK (gueltig_bis IS NULL OR gueltig_bis >= gueltig_von)
);

CREATE TABLE termin (
    id            uuid PRIMARY KEY DEFAULT gen_uuid_v7(),
    verein_id     uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    mannschaft_id uuid NOT NULL,
    serie_id      uuid,
    -- Kalendertag aus der Serie; bleibt gleich, wenn der Termin verschoben wird.
    serie_datum   date,
    typ           text NOT NULL CHECK (typ IN ('training', 'spiel', 'sonstiges')),
    titel         text NOT NULL DEFAULT '',
    beginn        timestamptz NOT NULL,
    ende          timestamptz NOT NULL,
    treffzeit     timestamptz,
    ort           text NOT NULL DEFAULT '',
    treffpunkt    text NOT NULL DEFAULT '',
    frist         timestamptz,
    abgesagt      boolean NOT NULL DEFAULT false,
    -- Einzeln geänderte Serientermine fasst die Serie nicht mehr an.
    bearbeitet    boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (verein_id, mannschaft_id) REFERENCES mannschaft (verein_id, id) ON DELETE CASCADE,
    FOREIGN KEY (verein_id, serie_id) REFERENCES terminserie (verein_id, id) ON DELETE SET NULL (serie_id),
    UNIQUE (verein_id, id),
    UNIQUE (serie_id, serie_datum),
    CHECK (ende > beginn),
    CHECK (treffzeit IS NULL OR treffzeit <= beginn),
    CHECK (frist IS NULL OR frist <= beginn)
);
CREATE INDEX termin_mannschaft_beginn ON termin (mannschaft_id, beginn);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['terminserie', 'termin'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format($p$CREATE POLICY verein_isolation ON %I
      USING (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid)
      WITH CHECK (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid)$p$, t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE termin;
DROP TABLE terminserie;
ALTER TABLE mannschaft DROP CONSTRAINT mannschaft_verein_id_id;
