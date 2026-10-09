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

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/config"
	"github.com/ruben-sch/teamtafel/internal/db"
	"github.com/ruben-sch/teamtafel/internal/job"
	"github.com/ruben-sch/teamtafel/internal/kalender"
	"github.com/ruben-sch/teamtafel/internal/mail"
	"github.com/ruben-sch/teamtafel/internal/nachricht"
	"github.com/ruben-sch/teamtafel/internal/push"
	"github.com/ruben-sch/teamtafel/internal/team"
	"github.com/ruben-sch/teamtafel/internal/termin"
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

	vereine, termine := verein.NewStore(pool), termin.NewStore(pool)
	mailer := mail.NewSMTP(mail.Config{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort,
		User: cfg.SMTPUser, Password: cfg.SMTPPassword,
		From: cfg.MailFrom,
	})
	zustellung := nachricht.NewZustellung(pool, mailer, cfg.Scheme, cfg.BaseHost, cfg.MailAllowlist)
	var abos *push.Store
	if cfg.VAPIDPublicKey != "" && cfg.VAPIDPrivateKey != "" {
		abos = push.NewStore(pool)
		zustellung.MitPush(abos, &push.Sender{
			PublicKey: cfg.VAPIDPublicKey, PrivateKey: cfg.VAPIDPrivateKey, Subject: cfg.VAPIDSubject})
	} else {
		slog.Info("web push aus: VAPID_PUBLIC_KEY oder VAPID_PRIVATE_KEY fehlt")
	}
	worker := job.NewWorker(pool)
	worker.Registrieren(nachricht.Art, zustellung.Zustellen)
	go worker.Laufen(ctx, 10*time.Second)
	go wartung(ctx, vereine, termine, worker)

	srv := &http.Server{
		Addr: cfg.ListenAddr,
		Handler: web.NewHandler(web.Options{
			DB:             pool,
			Vereine:        vereine,
			Auth:           auth.NewStore(pool),
			Team:           team.NewStore(pool),
			Termine:        termine,
			Mailer:         mailer,
			Superadmins:    cfg.Superadmins,
			Push:           pushAbos(abos),
			VAPIDPublicKey: cfg.VAPIDPublicKey,
			Kalender:       kalender.NewStore(pool),
			Scheme:         cfg.Scheme,
			BaseHost:       cfg.BaseHost,
			Version:        cfg.Version,
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

// pushAbos vermeidet ein typisiertes nil im Interface, wenn Push aus ist.
func pushAbos(s *push.Store) web.PushAbos {
	if s == nil {
		return nil
	}
	return s
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

// wartung erinnert alle fünf Minuten an offene Rückmeldungen und hält stündlich die
// Serientermine acht Wochen im Voraus vor. Mehrere Instanzen stören sich nicht:
// Fortschreiben ist idempotent, Erinnern markiert jeden Termin in derselben Transaktion.
func wartung(ctx context.Context, vereine *verein.Store, termine *termin.Store, worker *job.Worker) {
	for runde := 0; ; runde++ {
		vs, err := vereine.Alle(ctx)
		if err != nil {
			slog.Error("wartung: vereine laden", "err", err)
		}
		for _, v := range vs {
			if runde%12 == 0 {
				if err := termine.Fortschreiben(ctx, v.ID); err != nil {
					slog.Error("serien fortschreiben", "verein", v.Slug, "err", err)
				}
			}
			if err := termine.Erinnern(ctx, v.ID); err != nil {
				slog.Error("erinnern", "verein", v.Slug, "err", err)
			}
		}
		if runde%12 == 0 {
			if err := worker.Aufraeumen(ctx, 30*24*time.Hour); err != nil {
				slog.Error("jobs aufräumen", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
		}
	}
}
