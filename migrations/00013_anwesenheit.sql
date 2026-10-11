-- +goose Up
-- Anwesenheit, die der Trainer nach Beginn eines Termins abweichend von der Rückmeldung festhält.
-- Ohne Eintrag gilt: Zusage heißt anwesend, Absage oder keine Rückmeldung heißt nicht anwesend.
CREATE TABLE anwesenheit (
    verein_id    uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    termin_id    uuid NOT NULL,
    spieler_id   uuid NOT NULL REFERENCES spieler (id) ON DELETE CASCADE,
    anwesend     boolean NOT NULL,
    von_konto_id uuid REFERENCES konto (id) ON DELETE SET NULL,
    geaendert_am timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (termin_id, spieler_id),
    FOREIGN KEY (verein_id, termin_id) REFERENCES termin (verein_id, id) ON DELETE CASCADE
);

ALTER TABLE anwesenheit ENABLE ROW LEVEL SECURITY;
CREATE POLICY verein_isolation ON anwesenheit
    USING (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid)
    WITH CHECK (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid);

-- +goose Down
DROP TABLE anwesenheit;
