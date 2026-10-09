package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/ruben-sch/teamtafel/internal/config"
	"github.com/ruben-sch/teamtafel/internal/db"
	"github.com/ruben-sch/teamtafel/internal/verein"
)

const adminUsage = `verwendung:
  teamtafel verein-anlegen <slug> <name>
  teamtafel mannschaft-anlegen <verein-slug> <saison JJJJ/JJ> <name>`

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
	}
	return nil
}
