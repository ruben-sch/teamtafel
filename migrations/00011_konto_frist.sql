-- +goose Up
-- Löschfrist für Konten ohne Verknüpfung: Die Wartung merkt sich, seit wann ein Konto
-- in keinem Verein mehr Trainer, Admin, Spieler oder Vertretung ist und wann der Hinweis
-- vor der Löschung verschickt wurde. Wird das Konto wieder verknüpft, fallen beide weg.
ALTER TABLE konto ADD COLUMN unverknuepft_seit timestamptz;
ALTER TABLE konto ADD COLUMN hinweis_am timestamptz;

-- +goose Down
ALTER TABLE konto DROP COLUMN hinweis_am;
ALTER TABLE konto DROP COLUMN unverknuepft_seit;
