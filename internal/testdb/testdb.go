// Package testdb gives tests a fresh, migrated PostgreSQL database.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rustambekabdusalomov/shop-api/migrations"
)

// EnvURL names the environment variable with the admin connection URL. The role needs CREATEDB.
const EnvURL = "SHOP_TEST_DATABASE_URL"

// setupStatements create what production gets from deploy/roles.sql (the schemas) and from
// billing-worker's migrations (billing.jobs).
var setupStatements = []string{
	`CREATE SCHEMA shop`,
	`CREATE SCHEMA billing`,
	`CREATE TABLE billing.jobs (
	    id         BIGSERIAL   PRIMARY KEY,
	    kind       TEXT        NOT NULL,
	    payload    JSONB       NOT NULL,
	    status     TEXT        NOT NULL DEFAULT 'queued',
	    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
}

// New creates a uniquely named database, creates the schemas, runs the migrations (twice,
// so re-running them is checked) and returns a pool on it. The database is dropped when the
// test ends. The test fails when SHOP_TEST_DATABASE_URL is not set.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	adminURL := os.Getenv(EnvURL)
	if adminURL == "" {
		t.Fatalf("%s is not set: database tests need a PostgreSQL server whose role has CREATEDB", EnvURL)
	}
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect to %s: %v", EnvURL, err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		admin.Close()
		t.Fatalf("random database name: %v", err)
	}
	name := "shop_test_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatalf("create database %s: %v", name, err)
	}

	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.Exec(dropCtx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop database %s: %v", name, err)
		}
		admin.Close()
	})

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse %s (it must be a postgres:// URL): %v", EnvURL, err)
	}
	u.Path = "/" + name
	dsn := u.String()

	pool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to %s: %v", name, err)
	}
	for _, stmt := range setupStatements {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("set up %s: %v", name, err)
		}
	}
	for i := 1; i <= 2; i++ {
		if err := migrations.Up(ctx, dsn); err != nil {
			t.Fatalf("migrate (run %d): %v", i, err)
		}
	}
	return pool
}

// InsertProduct adds a product for a test and returns its id.
func InsertProduct(t testing.TB, pool *pgxpool.Pool, sku string, priceCents int64, stock int) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO shop.products (sku, name, description, price_cents, image_url, stock)
		 VALUES ($1, $2, '', $3, '/img/test.jpg', $4)
		 RETURNING id`, sku, "Test "+sku, priceCents, stock,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert product %s: %v", sku, err)
	}
	return id
}
