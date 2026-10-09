-- +goose Up
-- Outbox und Job-Queue. Global ohne verein_id, damit der Worker vereinsübergreifend
-- abarbeiten kann; Jobs entstehen in derselben Transaktion wie die fachliche Änderung.
CREATE TABLE job (
    id                uuid PRIMARY KEY DEFAULT gen_uuid_v7(),
    art               text NOT NULL,
    payload           jsonb NOT NULL,
    faellig_ab        timestamptz NOT NULL DEFAULT now(),
    versuche          int NOT NULL DEFAULT 0,
    letzter_fehler    text,
    erledigt_am       timestamptz,
    fehlgeschlagen_am timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX job_faellig ON job (faellig_ab) WHERE erledigt_am IS NULL AND fehlgeschlagen_am IS NULL;

-- Erinnerung 24 Stunden vor der Frist geht je Termin nur einmal raus.
ALTER TABLE termin ADD COLUMN erinnert_am timestamptz;

-- +goose Down
ALTER TABLE termin DROP COLUMN erinnert_am;
DROP TABLE job;
