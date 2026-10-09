-- +goose Up
-- Zu- oder Absage eines Spielers zu einem Termin. Keine Zeile heißt offen;
-- eine Änderung überschreibt die Zeile, die letzte gewinnt.
CREATE TABLE rueckmeldung (
    verein_id    uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    termin_id    uuid NOT NULL,
    spieler_id   uuid NOT NULL REFERENCES spieler (id) ON DELETE CASCADE,
    status       text NOT NULL CHECK (status IN ('zu', 'ab')),
    -- Nur bei Absagen; kein Freitext, damit keine Gesundheitsdetails landen.
    grund        text CHECK (grund IN ('krank', 'urlaub', 'sonstiges')),
    von_konto_id uuid REFERENCES konto (id) ON DELETE SET NULL,
    geaendert_am timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (termin_id, spieler_id),
    FOREIGN KEY (verein_id, termin_id) REFERENCES termin (verein_id, id) ON DELETE CASCADE,
    CHECK (status = 'ab' OR grund IS NULL)
);

ALTER TABLE rueckmeldung ENABLE ROW LEVEL SECURITY;
CREATE POLICY verein_isolation ON rueckmeldung
    USING (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid)
    WITH CHECK (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid);

-- +goose Down
DROP TABLE rueckmeldung;
