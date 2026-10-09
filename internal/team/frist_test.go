package team_test

import (
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/db"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

func aufnehmen(t *testing.T, f fixture, mannschaftID string, k auth.Konto, art, vorname string, gleicher bool) {
	t.Helper()
	a, err := f.store.AnfrageStellen(f.ctx, f.verein.ID, mannschaftID, k.ID,
		team.AnfrageDaten{Art: art, Vorname: vorname, Nachname: "Frist", Jahrgang: 2018})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Freigeben(f.ctx, f.verein.ID, a.ID, f.trainer.ID, gleicher); err != nil {
		t.Fatal(err)
	}
}

func zaehlen(t *testing.T, f fixture, q string, args ...any) int {
	t.Helper()
	var n int
	err := db.InVerein(f.ctx, f.pool, f.verein.ID, func(tx pgx.Tx) error {
		return tx.QueryRow(f.ctx, q, args...).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSpielerOhneKaderNachSechsMonatenGeloescht(t *testing.T) {
	f := setup(t) // Bambini in 2026/27, Saisonende 30.6.2027
	naechste, err := verein.NewStore(f.pool).MannschaftAnlegen(f.ctx, f.verein.ID, "2027/28", "F-Jugend")
	if err != nil {
		t.Fatal(err)
	}
	aufnehmen(t, f, f.bambini.ID, f.eltern, team.ArtKind, "Mia", false)
	aufnehmen(t, f, f.bambini.ID, f.eltern, team.ArtKind, "Leo", false)
	aufnehmen(t, f, naechste.ID, f.eltern, team.ArtKind, "Leo", true)
	// Spieler ohne jeden Kader zählt ab dem Anlegen.
	err = db.InVerein(f.ctx, f.pool, f.verein.ID, func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, `INSERT INTO spieler (verein_id, vorname, nachname, jahrgang, created_at)
			VALUES ($1, 'Ohne', 'Frist', 2018, '2020-01-01')`, f.verein.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	spieler := func(vorname string) int {
		return zaehlen(t, f, `SELECT count(*) FROM spieler WHERE vorname = $1`, vorname)
	}

	if err := f.store.SpielerOhneKaderLoeschen(f.ctx, f.verein.ID, time.Date(2027, 12, 30, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if spieler("Ohne") != 0 || spieler("Mia") != 1 || spieler("Leo") != 1 {
		t.Fatalf("nach 30.12.2027: ohne %d, mia %d, leo %d", spieler("Ohne"), spieler("Mia"), spieler("Leo"))
	}

	if err := f.store.SpielerOhneKaderLoeschen(f.ctx, f.verein.ID, time.Date(2028, 1, 2, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if spieler("Mia") != 0 || spieler("Leo") != 1 {
		t.Fatalf("nach 2.1.2028: mia %d, leo %d", spieler("Mia"), spieler("Leo"))
	}
	if n := zaehlen(t, f, `SELECT count(*) FROM vertretung WHERE konto_id = $1`, f.eltern.ID); n != 1 {
		t.Errorf("vertretungen = %d, nur Leo bleibt", n)
	}
	// Freigegebene Anfragen der abgelaufenen Saison enthalten Namen und gehen mit.
	if n := zaehlen(t, f, `SELECT count(*) FROM beitrittsanfrage WHERE mannschaft_id = $1`, f.bambini.ID); n != 0 {
		t.Errorf("anfragen der alten saison = %d", n)
	}
	if n := zaehlen(t, f, `SELECT count(*) FROM beitrittsanfrage WHERE mannschaft_id = $1`, naechste.ID); n != 1 {
		t.Errorf("anfragen der laufenden saison = %d", n)
	}
}

func TestVerknuepfteKonten(t *testing.T) {
	f := setup(t)
	selbst, offen, admin, lose := konto(t, f.pool), konto(t, f.pool), konto(t, f.pool), konto(t, f.pool)
	aufnehmen(t, f, f.bambini.ID, f.eltern, team.ArtKind, "Mia", false)
	aufnehmen(t, f, f.bambini.ID, selbst, team.ArtSelbst, "Tom", false)
	if _, err := f.store.AnfrageStellen(f.ctx, f.verein.ID, f.bambini.ID, offen.ID,
		team.AnfrageDaten{Art: team.ArtKind, Vorname: "Ida", Nachname: "Frist", Jahrgang: 2019}); err != nil {
		t.Fatal(err)
	}
	if err := verein.NewStore(f.pool).AdminHinzufuegen(f.ctx, f.verein.ID, admin.ID); err != nil {
		t.Fatal(err)
	}

	got, err := f.store.VerknuepfteKonten(f.ctx, f.verein.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []auth.Konto{f.trainer, f.eltern, selbst, offen, admin} {
		if !slices.Contains(got, k.ID) {
			t.Errorf("%s fehlt in %v", k.Email, got)
		}
	}
	if slices.Contains(got, lose.ID) {
		t.Error("unverknüpftes konto enthalten")
	}
}
