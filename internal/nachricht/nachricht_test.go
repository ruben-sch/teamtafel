package nachricht_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/db"
	"github.com/ruben-sch/teamtafel/internal/dbtest"
	"github.com/ruben-sch/teamtafel/internal/nachricht"
	"github.com/ruben-sch/teamtafel/internal/push"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

func eindeutig(p string) string { return fmt.Sprintf("%s-%d", p, time.Now().UnixNano()) }

type welt struct {
	ctx                            context.Context
	pool                           *pgxpool.Pool
	verein                         verein.Verein
	mannschaft                     string
	trainer, eltern, selbst, fremd auth.Konto
	kind                           string
}

// Trainer, Eltern mit Kind, ein Spieler mit eigenem Konto; fremd ist in einer anderen Mannschaft.
func aufbauen(t *testing.T) *welt {
	t.Helper()
	pool := dbtest.AppPool(t)
	ctx := context.Background()
	vs, as, ts := verein.NewStore(pool), auth.NewStore(pool), team.NewStore(pool)
	v, err := vs.Anlegen(ctx, eindeutig("fc"), "FC Nachricht")
	if err != nil {
		t.Fatal(err)
	}
	m, _ := vs.MannschaftAnlegen(ctx, v.ID, "2026/27", "E1")
	andere, _ := vs.MannschaftAnlegen(ctx, v.ID, "2026/27", "E2")
	w := &welt{ctx: ctx, pool: pool, verein: v, mannschaft: m.ID}
	for _, p := range []*auth.Konto{&w.trainer, &w.eltern, &w.selbst, &w.fremd} {
		if *p, err = as.KontoFuer(ctx, eindeutig("n")+"@example.org"); err != nil {
			t.Fatal(err)
		}
	}
	_ = ts.TrainerHinzufuegen(ctx, v.ID, m.ID, w.trainer.ID)
	_ = ts.TrainerHinzufuegen(ctx, v.ID, andere.ID, w.fremd.ID)
	for _, a := range []struct {
		konto auth.Konto
		art   string
	}{{w.eltern, team.ArtKind}, {w.selbst, team.ArtSelbst}} {
		an, err := ts.AnfrageStellen(ctx, v.ID, m.ID, a.konto.ID, team.AnfrageDaten{Art: a.art, Vorname: "Kim", Nachname: eindeutig("x"), Jahrgang: 2016})
		if err != nil {
			t.Fatal(err)
		}
		if err := ts.Freigeben(ctx, v.ID, an.ID, w.trainer.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	sp, _ := ts.MeineSpieler(ctx, v.ID, w.eltern.ID)
	w.kind = sp[0].ID
	return w
}

func (w *welt) tx(t *testing.T, fn func(pgx.Tx) error) {
	t.Helper()
	if err := db.InVerein(w.ctx, w.pool, w.verein.ID, fn); err != nil {
		t.Fatal(err)
	}
}

func sortiert(ks ...string) []string { slices.Sort(ks); return ks }

func TestEmpfaenger(t *testing.T) {
	w := aufbauen(t)
	w.tx(t, func(tx pgx.Tx) error {
		team, err := nachricht.Team(w.ctx, tx, w.mannschaft, w.trainer.ID)
		if err != nil {
			return err
		}
		slices.Sort(team)
		if want := sortiert(w.eltern.ID, w.selbst.ID); !slices.Equal(team, want) {
			t.Errorf("team ohne trainer = %v, want %v", team, want)
		}
		tr, _ := nachricht.Trainer(w.ctx, tx, w.mannschaft, "")
		if !slices.Equal(tr, []string{w.trainer.ID}) {
			t.Errorf("trainer = %v", tr)
		}
		sp, _ := nachricht.Spieler(w.ctx, tx, w.kind, "")
		if !slices.Equal(sp, []string{w.eltern.ID}) {
			t.Errorf("spieler = %v", sp)
		}
		return nil
	})
}

type gesendet struct{ an, betreff, text string }

type fakeMailer struct {
	mails []gesendet
	err   error
}

func (f *fakeMailer) Senden(_ context.Context, an, betreff, text string) error {
	if f.err != nil {
		return f.err
	}
	f.mails = append(f.mails, gesendet{an, betreff, text})
	return nil
}

// payloads liest die eingereihten Benachrichtigungen eines Kontos.
func payloads(t *testing.T, w *welt, konto string) [][]byte {
	t.Helper()
	rows, _ := w.pool.Query(w.ctx, `SELECT payload FROM job WHERE art = $1 AND payload->>'konto_id' = $2 ORDER BY created_at`,
		nachricht.Art, konto)
	ps, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestAnUndZustellen(t *testing.T) {
	w := aufbauen(t)
	in := nachricht.Inhalt{Betreff: "Training abgesagt", Text: "Das Training fällt aus.", Pfad: "/t/123"}
	w.tx(t, func(tx pgx.Tx) error {
		return nachricht.An(w.ctx, tx, w.verein.ID, []string{w.eltern.ID, w.eltern.ID, w.selbst.ID}, in, time.Now())
	})
	ps := payloads(t, w, w.eltern.ID)
	if len(ps) != 1 {
		t.Fatalf("eltern: %d jobs, erwartet 1 (Dublette entfernt)", len(ps))
	}
	var p struct{ Verein string }
	_ = json.Unmarshal(ps[0], &p)
	if p.Verein != w.verein.Slug {
		t.Errorf("verein %q", p.Verein)
	}

	m := &fakeMailer{}
	z := nachricht.NewZustellung(w.pool, m, "https", "teamtafel.example", nil)
	if err := z.Zustellen(w.ctx, ps[0]); err != nil {
		t.Fatal(err)
	}
	if len(m.mails) != 1 || m.mails[0].an != w.eltern.Email || m.mails[0].betreff != in.Betreff {
		t.Fatalf("mails %+v", m.mails)
	}
	if link := "https://" + w.verein.Slug + ".teamtafel.example/t/123"; !strings.Contains(m.mails[0].text, link) {
		t.Errorf("link %s fehlt in %q", link, m.mails[0].text)
	}

	// Allowlist (Staging): nicht freigegebene Adressen werden still übersprungen.
	m2 := &fakeMailer{}
	z2 := nachricht.NewZustellung(w.pool, m2, "https", "x", []string{strings.ToUpper(w.selbst.Email)})
	_ = z2.Zustellen(w.ctx, ps[0])
	_ = z2.Zustellen(w.ctx, payloads(t, w, w.selbst.ID)[0])
	if len(m2.mails) != 1 || m2.mails[0].an != w.selbst.Email {
		t.Errorf("allowlist: %+v", m2.mails)
	}

	// SMTP-Fehler wird an den Worker gemeldet, damit er erneut versucht.
	z3 := nachricht.NewZustellung(w.pool, &fakeMailer{err: errors.New("weg")}, "https", "x", nil)
	if err := z3.Zustellen(w.ctx, ps[0]); err == nil {
		t.Error("fehler verschluckt")
	}
}

type fakeAbos struct {
	abos     []push.Abo
	entfernt []string
}

func (f *fakeAbos) Abos(context.Context, string) ([]push.Abo, error) { return f.abos, nil }
func (f *fakeAbos) Entfernen(_ context.Context, e string) error {
	f.entfernt = append(f.entfernt, e)
	return nil
}

type fakePusher struct {
	ergebnis map[string]error
	daten    []string
}

func (f *fakePusher) Senden(_ context.Context, a push.Abo, data []byte) error {
	f.daten = append(f.daten, string(data))
	return f.ergebnis[a.Endpoint]
}

func aboMit(endpoint string) push.Abo { var a push.Abo; a.Endpoint = endpoint; return a }

func TestPushZuerstSonstMail(t *testing.T) {
	w := aufbauen(t)
	einreihen := func(in nachricht.Inhalt) []byte {
		w.tx(t, func(tx pgx.Tx) error {
			return nachricht.An(w.ctx, tx, w.verein.ID, []string{w.eltern.ID}, in, time.Now())
		})
		ps := payloads(t, w, w.eltern.ID)
		return ps[len(ps)-1]
	}
	normal := einreihen(nachricht.Inhalt{Betreff: "Neu: Training", Text: "Neuer Termin", Pfad: "/t/1"})
	dringend := einreihen(nachricht.Inhalt{Betreff: "Abgesagt", Text: "fällt aus", Pfad: "/t/1", Dringend: true})

	fall := func(name string, abos []push.Abo, ergebnis map[string]error, p []byte, wantMails int) (*fakeAbos, *fakePusher) {
		t.Helper()
		m, fa, fp := &fakeMailer{}, &fakeAbos{abos: abos}, &fakePusher{ergebnis: ergebnis}
		z := nachricht.NewZustellung(w.pool, m, "https", "x.example", nil).MitPush(fa, fp)
		if err := z.Zustellen(w.ctx, p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(m.mails) != wantMails {
			t.Errorf("%s: %d mails, want %d", name, len(m.mails), wantMails)
		}
		return fa, fp
	}
	_, fp := fall("ein gerät erreicht", []push.Abo{aboMit("a"), aboMit("b")}, map[string]error{"a": errors.New("timeout")}, normal, 0)
	if len(fp.daten) != 2 || !strings.Contains(fp.daten[0], `"url":"https://`+w.verein.Slug+`.x.example/t/1"`) {
		t.Errorf("push-daten %q", fp.daten)
	}
	fa, _ := fall("abgelaufen", []push.Abo{aboMit("a")}, map[string]error{"a": push.ErrAbgelaufen}, normal, 1)
	if !slices.Equal(fa.entfernt, []string{"a"}) {
		t.Errorf("entfernt %v", fa.entfernt)
	}
	fall("ohne abo", nil, nil, normal, 1)
	fall("dringend", []push.Abo{aboMit("a")}, nil, dringend, 1)
}
