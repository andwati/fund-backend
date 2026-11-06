// Command fundd is the Fund API server.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/andwati/fund-backend/internal/api"
	"github.com/andwati/fund-backend/internal/auth"
	"github.com/andwati/fund-backend/internal/config"
	"github.com/andwati/fund-backend/internal/db"
	"github.com/andwati/fund-backend/internal/db/dbgen"
	"github.com/andwati/fund-backend/internal/fund"
	"github.com/andwati/fund-backend/internal/mail"
	"github.com/andwati/fund-backend/internal/ratelimit"
	"github.com/andwati/fund-backend/internal/reminders"
)

func main() {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	if err := run(cfg, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(cfg config.Config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.MigrateOnStart {
		sqlDB, err := sql.Open("pgx", cfg.DatabaseURL)
		if err != nil {
			return err
		}
		err = db.Up(ctx, sqlDB)
		sqlDB.Close()
		if err != nil {
			return err
		}
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	q := dbgen.New(pool)
	sender := &mail.Resend{APIKey: cfg.ResendKey, From: cfg.EmailFrom, Log: log}
	mailer := &mail.Mailer{
		Sender:    sender,
		PublicURL: cfg.PublicURL,
		HasAccount: func(ctx context.Context, email string) bool {
			_, err := q.GetUserByEmail(ctx, email)
			return err == nil
		},
	}
	limiter := ratelimit.New()
	authSvc := auth.NewService(auth.Options{Pool: pool, Mailer: mailer, Secret: cfg.AuthSecret, PublicURL: cfg.PublicURL,
		BeforeDelete: fund.PrepareAccountDeletion, Logger: log})
	fundSvc := fund.NewService(fund.Options{Pool: pool, Auth: authSvc, Mailer: mailer, Limiter: limiter, PublicURL: cfg.PublicURL, Logger: log})
	runner := &reminders.Runner{Q: q, Mail: mailer, Secret: []byte(cfg.AuthSecret), APIURL: cfg.APIURL, PublicURL: cfg.PublicURL, Log: log}

	handler := api.New(api.Deps{
		Pool: pool, Auth: authSvc, Fund: fundSvc, Reminders: runner, Limiter: limiter, Log: log,
		CronSecret: cfg.CronSecret, CORSOrigins: cfg.CORSOrigins, TrustProxy: cfg.TrustProxy, RateLimitEnabled: cfg.RateLimit,
	}).Handler()

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
