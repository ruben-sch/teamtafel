package team_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/db"
	"github.com/ruben-sch/teamtafel/internal/dbtest"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

type fixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	store   *team.Store
	verein  verein.Verein
	bambini verein.Mannschaft
	trainer auth.Konto
	eltern  auth.Konto
}

func eindeutig(prefix string) string { return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()) }

func konto(t *testing.T, pool *pgxpool.Pool) auth.Konto {
	t.Helper()
	k, err := auth.NewStore(pool).KontoFuer(context.Background(), eindeutig("p")+"@example.org")
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func setup(t *testing.T) fixture {
	t.Helper()
	pool := dbtest.AppPool(t)
	ctx := context.Background()
	vs := verein.NewStore(pool)
	v, err := vs.Anlegen(ctx, eindeutig("fc"), "FC Test")
	if err != nil {
		t.Fatal(err)
	}
	m, err := vs.MannschaftAnlegen(ctx, v.ID, "2026/27", "Bambini")
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{ctx: ctx, pool: pool, store: team.NewStore(pool), verein: v, bambini: m,
		trainer: konto(t, pool), eltern: konto(t, pool)}
	if err := f.store.TrainerHinzufuegen(ctx, v.ID, m.ID, f.trainer.ID); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestTrainerRolle(t *testing.T) {
	f := setup(t)
	if ok, err := f.store.IstTrainer(f.ctx, f.verein.ID, f.bambini.ID, f.trainer.ID); err != nil || !ok {
		t.Fatalf("trainer: ok=%v err=%v", ok, err)
	}
	if ok, _ := f.store.IstTrainer(f.ctx, f.verein.ID, f.bambini.ID, f.eltern.ID); ok {
		t.Fatal("eltern sind keine trainer")
	}
	ms, err := f.store.TrainerMannschaften(f.ctx, f.verein.ID, f.trainer.ID)
	if err != nil || len(ms) != 1 || ms[0].ID != f.bambini.ID {
		t.Fatalf("trainer-mannschaften = %+v, %v", ms, err)
	}
}

func TestEinladungErneuernMachtAltenLinkUngueltig(t *testing.T) {
	f := setup(t)
	alt, err := f.store.EinladungErneuern(f.ctx, f.verein.ID, f.bambini.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := f.store.Einladung(f.ctx, f.verein.ID, alt)
	if err != nil || m.ID != f.bambini.ID {
		t.Fatalf("einladung = %+v, %v", m, err)
	}
	neu, _ := f.store.EinladungErneuern(f.ctx, f.verein.ID, f.bambini.ID)
	if _, err := f.store.Einladung(f.ctx, f.verein.ID, alt); !errors.Is(err, team.ErrEinladungUngueltig) {
		t.Fatalf("alter link: err = %v", err)
	}
	if _, err := f.store.Einladung(f.ctx, f.verein.ID, neu); err != nil {
		t.Fatalf("neuer link: %v", err)
	}
	// Ein Link gilt nur im eigenen Verein.
	anderer, _ := verein.NewStore(f.pool).Anlegen(f.ctx, eindeutig("x"), "Anderer")
	if _, err := f.store.Einladung(f.ctx, anderer.ID, neu); !errors.Is(err, team.ErrEinladungUngueltig) {
		t.Fatalf("fremder verein: err = %v", err)
	}
}

func TestAnfrageOhneTrefferLegtSpielerAn(t *testing.T) {
	f := setup(t)
	a, err := f.store.AnfrageStellen(f.ctx, f.verein.ID, f.bambini.ID, f.eltern.ID,
		team.AnfrageDaten{Art: team.ArtKind, Vorname: "Mia", Nachname: "Muster", Jahrgang: 2020})
	if err != nil {
		t.Fatal(err)
	}
	if a.TrefferSpielerID != "" {
		t.Fatal("unerwarteter treffer")
	}
	offen, _ := f.store.OffeneAnfragen(f.ctx, f.verein.ID, f.bambini.ID)
	if len(offen) != 1 || offen[0].Email != f.eltern.Email {
		t.Fatalf("offene anfragen = %+v", offen)
	}

	if err := f.store.Freigeben(f.ctx, f.verein.ID, a.ID, f.trainer.ID, false); err != nil {
		t.Fatal(err)
	}
	kader, _ := f.store.Kader(f.ctx, f.verein.ID, f.bambini.ID)
	if len(kader) != 1 || kader[0].Vorname != "Mia" || kader[0].Jahrgang != 2020 {
		t.Fatalf("kader = %+v", kader)
	}
	meine, _ := f.store.MeineSpieler(f.ctx, f.verein.ID, f.eltern.ID)
	if len(meine) != 1 || meine[0].ID != kader[0].ID {
		t.Fatalf("eltern vertreten = %+v", meine)
	}
	if offen, _ := f.store.OffeneAnfragen(f.ctx, f.verein.ID, f.bambini.ID); len(offen) != 0 {
		t.Fatal("anfrage noch offen")
	}
}

func TestZweitesElternteilWirdAlsTrefferErkannt(t *testing.T) {
	f := setup(t)
	erste, _ := f.store.AnfrageStellen(f.ctx, f.verein.ID, f.bambini.ID, f.eltern.ID,
		team.AnfrageDaten{Art: team.ArtKind, Vorname: "Mia", Nachname: "Muster", Jahrgang: 2020})
	_ = f.store.Freigeben(f.ctx, f.verein.ID, erste.ID, f.trainer.ID, false)

	papa := konto(t, f.pool)
	a, err := f.store.AnfrageStellen(f.ctx, f.verein.ID, f.bambini.ID, papa.ID,
		team.AnfrageDaten{Art: team.ArtKind, Vorname: " mia ", Nachname: "MUSTER", Jahrgang: 2020})
	if err != nil {
		t.Fatal(err)
	}
	if a.TrefferSpielerID == "" {
		t.Fatal("treffer nicht erkannt")
	}
	if err := f.store.Freigeben(f.ctx, f.verein.ID, a.ID, f.trainer.ID, true); err != nil {
		t.Fatal(err)
	}
	kader, _ := f.store.Kader(f.ctx, f.verein.ID, f.bambini.ID)
	if len(kader) != 1 {
		t.Fatalf("bestätigter treffer darf keinen zweiten spieler anlegen: %+v", kader)
	}
	meine, _ := f.store.MeineSpieler(f.ctx, f.verein.ID, papa.ID)
	if len(meine) != 1 {
		t.Fatalf("papa vertritt %+v", meine)
	}
}

func TestSpielerMitEigenemKonto(t *testing.T) {
	f := setup(t)
	ich := konto(t, f.pool)
	a, _ := f.store.AnfrageStellen(f.ctx, f.verein.ID, f.bambini.ID, ich.ID,
		team.AnfrageDaten{Art: team.ArtSelbst, Vorname: "Tom", Nachname: "Groß", Jahrgang: 1990})
	if err := f.store.Freigeben(f.ctx, f.verein.ID, a.ID, f.trainer.ID, false); err != nil {
		t.Fatal(err)
	}
	meine, _ := f.store.MeineSpieler(f.ctx, f.verein.ID, ich.ID)
	if len(meine) != 1 || !meine[0].Selbst {
		t.Fatalf("eigener spieler = %+v", meine)
	}
}

func TestAblehnenLoeschtAnfrage(t *testing.T) {
	f := setup(t)
	a, _ := f.store.AnfrageStellen(f.ctx, f.verein.ID, f.bambini.ID, f.eltern.ID,
		team.AnfrageDaten{Art: team.ArtKind, Vorname: "Max", Nachname: "Weg", Jahrgang: 2020})
	if err := f.store.Ablehnen(f.ctx, f.verein.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	n := -1
	if err := dbtestCount(f, `SELECT count(*) FROM beitrittsanfrage WHERE vorname = 'Max' AND nachname = 'Weg'`, &n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("abgelehnte anfrage muss samt daten gelöscht sein")
	}
	if err := f.store.Freigeben(f.ctx, f.verein.ID, a.ID, f.trainer.ID, false); !errors.Is(err, team.ErrAnfrageUnbekannt) {
		t.Fatalf("freigeben nach ablehnen: err = %v", err)
	}
}

func TestAnfrageValidierung(t *testing.T) {
	f := setup(t)
	jetzt := time.Now().Year()
	for _, d := range []team.AnfrageDaten{
		{Art: "quatsch", Vorname: "A", Nachname: "B", Jahrgang: 2020},
		{Art: team.ArtKind, Vorname: "", Nachname: "B", Jahrgang: 2020},
		{Art: team.ArtKind, Vorname: "A", Nachname: " ", Jahrgang: 2020},
		{Art: team.ArtKind, Vorname: "A", Nachname: "B", Jahrgang: 1899},
		{Art: team.ArtKind, Vorname: "A", Nachname: "B", Jahrgang: jetzt + 1},
	} {
		if _, err := f.store.AnfrageStellen(f.ctx, f.verein.ID, f.bambini.ID, f.eltern.ID, d); !errors.Is(err, team.ErrUngueltigeAngaben) {
			t.Errorf("%+v: err = %v", d, err)
		}
	}
}

// dbtestCount zählt im Verein der Fixture; ohne gesetzten Verein lieferte RLS immer 0.
func dbtestCount(f fixture, q string, n *int) error {
	return db.InVerein(f.ctx, f.pool, f.verein.ID, func(tx pgx.Tx) error {
		return tx.QueryRow(f.ctx, q).Scan(n)
	})
}

func TestTrainerListeUndEntfernen(t *testing.T) {
	f := setup(t)
	tr, err := f.store.Trainer(f.ctx, f.verein.ID, f.bambini.ID)
	if err != nil || len(tr) != 1 || tr[0].KontoID != f.trainer.ID || tr[0].Email != f.trainer.Email {
		t.Fatalf("trainer = %+v %v", tr, err)
	}
	if err := f.store.TrainerEntfernen(f.ctx, f.verein.ID, f.bambini.ID, f.trainer.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.store.IstTrainer(f.ctx, f.verein.ID, f.bambini.ID, f.trainer.ID); ok {
		t.Fatal("noch trainer nach entfernen")
	}
}

func TestNeueAnfrageBenachrichtigtTrainerEinmal(t *testing.T) {
	f := setup(t)
	for range 2 {
		if _, err := f.store.AnfrageStellen(f.ctx, f.verein.ID, f.bambini.ID, f.eltern.ID,
			team.AnfrageDaten{Art: team.ArtKind, Vorname: "Mia", Nachname: "Muster", Jahrgang: 2020}); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := f.pool.Query(f.ctx, `SELECT payload->>'betreff' FROM job WHERE art = 'nachricht' AND payload->>'konto_id' = $1`,
		f.trainer.ID)
	betreffe, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if want := "Bambini: Neue Beitrittsanfrage für Mia"; len(betreffe) != 1 || betreffe[0] != want {
		t.Errorf("betreffe = %q, want [%q]", betreffe, want)
	}
}
