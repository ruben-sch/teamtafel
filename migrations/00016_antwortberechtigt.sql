-- +goose Up
-- antwortberechtigt ist die einzige Regel, wer für einen Spieler zu- oder absagen darf:
-- das eigene Konto des Spielers, und solange er keins hat, seine Vertretungen (ohne Altersgrenze).
-- SECURITY INVOKER, damit die RLS-Policies der Basistabellen greifen.
CREATE FUNCTION antwortberechtigt(spieler_id uuid, konto_id uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY INVOKER AS $$
    SELECT EXISTS (SELECT 1 FROM spieler sp WHERE sp.id = $1 AND sp.konto_id = $2)
        OR EXISTS (SELECT 1 FROM spieler sp JOIN vertretung v ON v.spieler_id = sp.id
            WHERE sp.id = $1 AND sp.konto_id IS NULL AND v.konto_id = $2)
$$;

-- +goose Down
DROP FUNCTION antwortberechtigt(uuid, uuid);
