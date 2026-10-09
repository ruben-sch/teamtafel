// Teamtafel: Teamverwaltung für Vereine.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ruben-sch/teamtafel/internal/config"
	"github.com/ruben-sch/teamtafel/internal/db"
	"github.com/ruben-sch/teamtafel/internal/verein"
	"github.com/ruben-sch/teamtafel/internal/web"
)

// version wird beim Build per -ldflags gesetzt.
var version = "dev"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			os.Exit(healthcheck())
		case "verein-anlegen", "mannschaft-anlegen":
			if err := admin(os.Args[1], os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "fehler:", err)
				os.Exit(1)
			}
			return
		}
	}
	if err := run(); err != nil {
		slog.Error("abbruch", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv(version)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := db.Migrate(ctx, cfg.MigrateDatabaseURL); err != nil {
		return err
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	srv := &http.Server{
		Addr: cfg.ListenAddr,
		Handler: web.NewHandler(web.Options{
			DB:       pool,
			Vereine:  verein.NewStore(pool),
			BaseHost: cfg.BaseHost,
			Version:  cfg.Version,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server startet", "addr", cfg.ListenAddr, "version", cfg.Version)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("server fährt herunter")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("herunterfahren: %w", err)
		}
	}
	return nil
}

// healthcheck ruft /healthz des laufenden Servers auf. Gedacht für den
// Docker-HEALTHCHECK, weil das Distroless-Image kein curl enthält.
func healthcheck() int {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://localhost" + addr + "/healthz")
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
