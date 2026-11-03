package coupon

import (
	"errors"
	"testing"
	"time"

	"github.com/rustambekabdusalomov/shop-api/internal/httpx"
)

var now = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func lines(cents ...int64) []Line {
	items := make([]Line, 0, len(cents))
	for _, c := range cents {
		items = append(items, Line{SKU: "SH-100", Quantity: 1, UnitPriceCents: c})
	}
	return items
}

func TestPercentRounding(t *testing.T) {
	tests := []struct {
		name  string
		pct   int64
		items []Line
		want  int64
	}{
		{"15 percent of 1999 rounds half up", 15, lines(1999), 300},
		{"10 percent of 1005 is exactly 100.5 and rounds up", 10, lines(1005), 101},
		{"10 percent of 1004 rounds down", 10, lines(1004), 100},
		{"10 percent of 2000 is exact", 10, lines(2000), 200},
		{"100 percent is the whole subtotal", 100, lines(1999), 1999},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Coupon{Code: "TEST", Kind: KindPercent, Value: tt.pct}
			got, err := Discount(c, tt.items, now)
			if err != nil {
				t.Fatalf("Discount: %v", err)
			}
			if got != tt.want {
				t.Errorf("discount = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFixedCoupon(t *testing.T) {
	tests := []struct {
		name  string
		value int64
		items []Line
		want  int64
	}{
		{"below the subtotal", 500, lines(3000), 500},
		{"capped at the subtotal", 5000, lines(1200), 1200},
		{"equal to the subtotal", 1200, lines(1200), 1200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Coupon{Code: "TEAM50", Kind: KindFixed, Value: tt.value}
			got, err := Discount(c, tt.items, now)
			if err != nil {
				t.Fatalf("Discount: %v", err)
			}
			if got != tt.want {
				t.Errorf("discount = %d, want %d", got, tt.want)
			}
			if total := Subtotal(tt.items) - got; total < 0 {
				t.Errorf("total = %d, must not be negative", total)
			}
		})
	}
}

func TestNotApplicable(t *testing.T) {
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	tests := []struct {
		name string
		c    Coupon
		in   []Line
	}{
		{"expired", Coupon{Code: "EXPIRED5", Kind: KindPercent, Value: 5, ExpiresAt: &past}, lines(5000)},
		{"expires exactly now", Coupon{Code: "EXPIRED5", Kind: KindPercent, Value: 5, ExpiresAt: &now}, lines(5000)},
		{"below the minimum total", Coupon{Code: "FREESHIP", Kind: KindFixed, Value: 500, MinTotalCents: 3000, ExpiresAt: &future}, lines(2999)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Discount(tt.c, tt.in, now)
			var na *NotApplicableError
			if !errors.As(err, &na) {
				t.Fatalf("error = %v, want a NotApplicableError", err)
			}
			if !errors.Is(err, httpx.ErrUnprocessable) {
				t.Errorf("error %v does not map to 422", err)
			}
			if got != 0 {
				t.Errorf("discount = %d, want 0", got)
			}
		})
	}
}

func TestMinimumTotalMet(t *testing.T) {
	c := Coupon{Code: "FREESHIP", Kind: KindFixed, Value: 500, MinTotalCents: 3000}
	got, err := Discount(c, lines(3000), now)
	if err != nil {
		t.Fatalf("Discount: %v", err)
	}
	if got != 500 {
		t.Errorf("discount = %d, want 500", got)
	}
}

func TestNullOnlyForApplies(t *testing.T) {
	c := Coupon{Code: "WELCOME10", Kind: KindPercent, Value: 10, OnlyFor: nil}
	got, err := Discount(c, lines(2000, 3000), now)
	if err != nil {
		t.Fatalf("Discount: %v", err)
	}
	if got != 500 {
		t.Errorf("discount = %d, want 500", got)
	}
}

func TestUnknownKind(t *testing.T) {
	_, err := Discount(Coupon{Code: "ODD", Kind: "bogo", Value: 1}, lines(1000), now)
	if err == nil {
		t.Fatal("want an error for an unknown kind")
	}
	if errors.Is(err, httpx.ErrUnprocessable) {
		t.Errorf("an unknown kind is a data problem, not a 422: %v", err)
	}
}
