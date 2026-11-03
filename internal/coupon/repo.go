package coupon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is what Load needs; both *pgxpool.Pool and pgx.Tx satisfy it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Repo loads coupons from shop.coupons.
type Repo struct {
	db *pgxpool.Pool
}

// NewRepo returns a Repo on db.
func NewRepo(db *pgxpool.Pool) *Repo {
	return &Repo{db: db}
}

// ByCode loads the coupon with the given code (case-insensitive). An unknown code is a
// NotApplicableError.
func (r *Repo) ByCode(ctx context.Context, code string) (Coupon, error) {
	return Load(ctx, r.db, code)
}

// Load is ByCode on any connection, so a caller inside a transaction reads through it
// instead of taking a second connection from the pool.
func Load(ctx context.Context, q Querier, code string) (Coupon, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	var c Coupon
	err := q.QueryRow(ctx,
		`SELECT code, kind, value, min_total_cents, expires_at, only_for
		   FROM shop.coupons WHERE code = $1`, code,
	).Scan(&c.Code, &c.Kind, &c.Value, &c.MinTotalCents, &c.ExpiresAt, &c.OnlyFor)
	if errors.Is(err, pgx.ErrNoRows) {
		return Coupon{}, &NotApplicableError{Reason: "unknown coupon code"}
	}
	if err != nil {
		return Coupon{}, fmt.Errorf("load coupon: %w", err)
	}
	return c, nil
}
