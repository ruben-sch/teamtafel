package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/config"
	"github.com/ruben-sch/teamtafel/internal/db"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

const adminUsage = `verwendung:
  teamtafel verein-anlegen <slug> <name>
  teamtafel mannschaft-anlegen <verein-slug> <saison JJJJ/JJ> <name>
  teamtafel trainer-hinzufuegen <verein-slug> <mannschaft-name|id> <email>`

// admin führt Verwaltungsbefehle aus, bis es dafür eine Oberfläche gibt.
// Im Container: docker compose exec app /teamtafel verein-anlegen fc "FC Beispiel"
func admin(cmd string, args []string) error {
	cfg, err := config.FromEnv(version)
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := verein.NewStore(pool)

	switch cmd {
	case "verein-anlegen":
		if len(args) != 2 {
			return errors.New(adminUsage)
		}
		v, err := store.Anlegen(ctx, args[0], args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Verein %q angelegt: https://%s.%s\n", v.Name, v.Slug, cfg.BaseHost)
	case "mannschaft-anlegen":
		if len(args) != 3 {
			return errors.New(adminUsage)
		}
		v, err := store.BySlug(ctx, args[0])
		if err != nil {
			return err
		}
		m, err := store.MannschaftAnlegen(ctx, v.ID, args[1], args[2])
		if err != nil {
			return err
		}
		fmt.Printf("Mannschaft %q (%s) in %q angelegt\n", m.Name, m.Saison, v.Name)
	case "trainer-hinzufuegen":
		if len(args) != 3 {
			return errors.New(adminUsage)
		}
		return trainerHinzufuegen(ctx, store, auth.NewStore(pool), team.NewStore(pool), args[0], args[1], args[2])
	}
	return nil
}

// trainerHinzufuegen sucht die Mannschaft per ID oder Name (aktuellste Saison
// zuerst) und legt das Konto bei Bedarf an.
func trainerHinzufuegen(ctx context.Context, vs *verein.Store, as *auth.Store, ts *team.Store, slug, mannschaft, email string) error {
	v, err := vs.BySlug(ctx, slug)
	if err != nil {
		return err
	}
	ms, err := vs.Mannschaften(ctx, v.ID)
	if err != nil {
		return err
	}
	var treffer *verein.Mannschaft
	for i := range ms {
		if ms[i].ID == mannschaft || strings.EqualFold(ms[i].Name, mannschaft) {
			treffer = &ms[i]
			break
		}
	}
	if treffer == nil {
		return fmt.Errorf("mannschaft %q in %q nicht gefunden", mannschaft, v.Name)
	}
	k, err := as.KontoFuer(ctx, email)
	if err != nil {
		return err
	}
	if err := ts.TrainerHinzufuegen(ctx, v.ID, treffer.ID, k.ID); err != nil {
		return err
	}
	fmt.Printf("%s ist Trainer von %q (%s)\n", k.Email, treffer.Name, treffer.Saison)
	return nil
}
