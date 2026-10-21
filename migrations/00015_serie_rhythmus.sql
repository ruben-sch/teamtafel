-- +goose Up
-- Serien laufen wöchentlich, alle 14 Tage oder monatlich am n-ten Wochentag.
-- Der 14-Tage-Takt zählt ab dem ersten passenden Wochentag ab gueltig_von.
ALTER TABLE terminserie
    ADD COLUMN rhythmus text NOT NULL DEFAULT 'woechentlich'
        CHECK (rhythmus IN ('woechentlich', 'zweiwoechentlich', 'monatlich')),
    -- Nur bei monatlich: 1 bis 4 für die n-te Woche, 5 für die letzte im Monat.
    ADD COLUMN monatswoche smallint CHECK (monatswoche BETWEEN 1 AND 5),
    ADD CONSTRAINT terminserie_monatswoche CHECK ((rhythmus = 'monatlich') = (monatswoche IS NOT NULL));

-- +goose Down
ALTER TABLE terminserie DROP CONSTRAINT terminserie_monatswoche, DROP COLUMN monatswoche, DROP COLUMN rhythmus;
