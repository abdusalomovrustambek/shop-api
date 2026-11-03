// Command shop-api is the HTTP API of the Shop demo: catalog, cart, coupons and orders.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rustambekabdusalomov/shop-api/internal/auth"
	"github.com/rustambekabdusalomov/shop-api/internal/server"
	"github.com/rustambekabdusalomov/shop-api/migrations"
)

type config struct {
	databaseURL string
	jwksURL     string
	addr        string
	level       slog.Level
}

func main() {
	// `shop-api healthcheck` is the container health check: the image has no shell or curl.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(envOr("ADDR", ":8081")))
	}
	if err := run(); err != nil {
		slog.Error("shop-api stopped", "err", err)
		os.Exit(1)
	}
}

func loadConfig() (config, error) {
	cfg := config{
		databaseURL: os.Getenv("DATABASE_URL"),
		jwksURL:     os.Getenv("AUTH_JWKS_URL"),
		addr:        envOr("ADDR", ":8081"),
	}
	if cfg.databaseURL == "" {
		return config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.jwksURL == "" {
		return config{}, errors.New("AUTH_JWKS_URL is required")
	}
	if err := cfg.level.UnmarshalText([]byte(envOr("LOG_LEVEL", "info"))); err != nil {
		return config{}, fmt.Errorf("invalid LOG_LEVEL: %w", err)
	}
	return cfg, nil
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.level}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := migrations.Up(ctx, cfg.databaseURL); err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, cfg.databaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	srv := &http.Server{
		Addr: cfg.addr,
		Handler: server.New(server.Deps{
			Pool:     pool,
			Verifier: auth.NewVerifier(cfg.jwksURL, log),
			Log:      log,
			Stderr:   os.Stderr,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	log.Info("shop-api listening", "addr", cfg.addr)

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// healthcheck asks the running server for /healthz and returns the process exit code.
func healthcheck(addr string) int {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
