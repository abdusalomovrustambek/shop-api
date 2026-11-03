package coupon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
	code = strings.ToUpper(strings.TrimSpace(code))
	var c Coupon
	err := r.db.QueryRow(ctx,
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
