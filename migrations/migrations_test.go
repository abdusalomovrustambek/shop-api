package migrations_test

import (
	"context"
	"testing"

	"github.com/rustambekabdusalomov/shop-api/internal/testdb"
	"github.com/rustambekabdusalomov/shop-api/migrations"
)

func TestSeedAndRerun(t *testing.T) {
	pool := testdb.New(t) // migrated twice already
	ctx := context.Background()

	counts := func() map[string]int {
		t.Helper()
		got := map[string]int{}
		for name, query := range map[string]string{
			"products": `SELECT count(*) FROM shop.products`,
			"coupons":  `SELECT count(*) FROM shop.coupons`,
			"versions": `SELECT count(*) FROM shop.goose_db_version`,
		} {
			var n int
			if err := pool.QueryRow(ctx, query).Scan(&n); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
			got[name] = n
		}
		return got
	}

	before := counts()
	if before["products"] != 40 || before["coupons"] != 6 {
		t.Fatalf("seed has %d products and %d coupons, want 40 and 6", before["products"], before["coupons"])
	}

	if err := migrations.Up(ctx, pool.Config().ConnString()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	after := counts()
	for name, n := range before {
		if after[name] != n {
			t.Errorf("%s changed from %d to %d after re-running the migrations", name, n, after[name])
		}
	}
}

func TestCouponsMatchTheRules(t *testing.T) {
	pool := testdb.New(t)

	var onlyFor, nullOnlyFor, expired int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FILTER (WHERE only_for IS NOT NULL),
		        count(*) FILTER (WHERE only_for IS NULL),
		        count(*) FILTER (WHERE expires_at < now())
		   FROM shop.coupons`).Scan(&onlyFor, &nullOnlyFor, &expired)
	if err != nil {
		t.Fatalf("query coupons: %v", err)
	}
	if onlyFor != 1 || nullOnlyFor != 5 || expired != 1 {
		t.Errorf("coupons with only_for = %d, without = %d, expired = %d; want 1, 5, 1", onlyFor, nullOnlyFor, expired)
	}
}
