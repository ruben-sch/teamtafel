-- +goose Up
-- Akzentfarbe der Oberfläche (Kopfleiste, Hauptknöpfe). Setzt der Vereinsadmin.
ALTER TABLE verein ADD COLUMN farbe text NOT NULL DEFAULT '#1F3A5F'
    CHECK (farbe ~ '^#[0-9A-F]{6}$');

-- +goose Down
ALTER TABLE verein DROP COLUMN farbe;
