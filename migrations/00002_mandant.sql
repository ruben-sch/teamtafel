-- +goose Up
-- UUIDv7 (zeitlich sortierbar). PostgreSQL 17 hat noch kein uuidv7().
CREATE FUNCTION gen_uuid_v7() RETURNS uuid
LANGUAGE sql VOLATILE AS $$
  SELECT encode(
    set_bit(set_bit(
      overlay(uuid_send(gen_random_uuid())
              PLACING substring(int8send(floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint) FROM 3)
              FROM 1 FOR 6),
      52, 1), 53, 1),
    'hex')::uuid
$$;

ALTER TABLE verein ALTER COLUMN id SET DEFAULT gen_uuid_v7();

CREATE TABLE saison (
    id         uuid PRIMARY KEY DEFAULT gen_uuid_v7(),
    verein_id  uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    name       text NOT NULL,
    beginn     date NOT NULL,
    ende       date NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (verein_id, name),
    UNIQUE (verein_id, id),
    CHECK (ende > beginn)
);

CREATE TABLE mannschaft (
    id         uuid PRIMARY KEY DEFAULT gen_uuid_v7(),
    verein_id  uuid NOT NULL REFERENCES verein (id) ON DELETE CASCADE,
    saison_id  uuid NOT NULL,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    -- Saison muss zum selben Verein gehören.
    FOREIGN KEY (verein_id, saison_id) REFERENCES saison (verein_id, id) ON DELETE CASCADE,
    UNIQUE (saison_id, name)
);

-- Mandantentrennung. Ohne gesetztes app.verein_id liefert current_setting NULL,
-- nach einer früheren Transaktion auf derselben Verbindung aber ''. NULLIF macht
-- beides zu NULL, die Policy trifft dann keine Zeile.
ALTER TABLE saison ENABLE ROW LEVEL SECURITY;
CREATE POLICY verein_isolation ON saison
    USING (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid)
    WITH CHECK (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid);

ALTER TABLE mannschaft ENABLE ROW LEVEL SECURITY;
CREATE POLICY verein_isolation ON mannschaft
    USING (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid)
    WITH CHECK (verein_id = NULLIF(current_setting('app.verein_id', true), '')::uuid);

-- +goose Down
DROP TABLE mannschaft;
DROP TABLE saison;
ALTER TABLE verein ALTER COLUMN id SET DEFAULT gen_random_uuid();
DROP FUNCTION gen_uuid_v7();
