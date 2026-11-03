// Package migrations holds the goose SQL migrations of schema shop and runs them.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sync"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var files embed.FS

// versionTable keeps goose's bookkeeping inside the schema the application owns.
const versionTable = "shop.goose_db_version"

// goose keeps its settings in package-level state.
var mu sync.Mutex

// Up applies all pending migrations to the database at dsn. The schema shop must exist
// and be writable by the connecting role. Running it again changes nothing.
func Up(ctx context.Context, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open database for migrations: %w", err)
	}
	defer func() { _ = db.Close() }()

	mu.Lock()
	defer mu.Unlock()
	goose.SetBaseFS(files)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	goose.SetTableName(versionTable)
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
