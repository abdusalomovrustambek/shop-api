// Package coupon holds the coupon rules. All money is integer cents.
package coupon

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/rustambekabdusalomov/shop-api/internal/httpx"
)

// Coupon kinds.
const (
	KindPercent = "percent"
	KindFixed   = "fixed"
)

// Coupon is a row of shop.coupons. OnlyFor is a JSON array of SKUs, or empty when the
// coupon applies to every product.
type Coupon struct {
	Code          string
	Kind          string
	Value         int64
	MinTotalCents int64
	ExpiresAt     *time.Time
	OnlyFor       []byte
}

// Line is one cart or order line as the rules see it.
type Line struct {
	SKU            string
	Quantity       int
	UnitPriceCents int64
}

// NotApplicableError says why a coupon cannot be used. It maps to 422.
type NotApplicableError struct {
	Reason string
}

func (e *NotApplicableError) Error() string { return "coupon not applicable: " + e.Reason }

// Is makes the error an httpx.ErrUnprocessable.
func (e *NotApplicableError) Is(target error) bool { return target == httpx.ErrUnprocessable }

// Subtotal returns the sum of quantity times unit price over items.
func Subtotal(items []Line) int64 {
	var sum int64
	for _, it := range items {
		sum += int64(it.Quantity) * it.UnitPriceCents
	}
	return sum
}

// Discount returns the discount c gives on items at now. It is never above the subtotal.
func Discount(c Coupon, items []Line, now time.Time) (int64, error) {
	return applyCoupon(c, items, Subtotal(items), now)
}

func applyCoupon(c Coupon, items []Line, subtotal int64, now time.Time) (discount int64, err error) {
	if c.ExpiresAt != nil && !now.Before(*c.ExpiresAt) {
		return 0, &NotApplicableError{Reason: "coupon " + c.Code + " has expired"}
	}
	if subtotal < c.MinTotalCents {
		return 0, &NotApplicableError{Reason: fmt.Sprintf("coupon %s needs a total of at least %d cents", c.Code, c.MinTotalCents)}
	}

	eligible := subtotal
	if len(c.OnlyFor) > 0 {
		var skus []string
		if err := json.Unmarshal(c.OnlyFor, &skus); err != nil {
			return 0, fmt.Errorf("decode only_for of coupon %s: %w", c.Code, err)
		}
		if len(skus) > 0 {
			var allowed skuSet
			for _, sku := range skus {
				allowed.add(sku)
			}
			eligible = 0
			for _, it := range items {
				if allowed.has(it.SKU) {
					eligible += int64(it.Quantity) * it.UnitPriceCents
				}
			}
			if eligible == 0 {
				return 0, &NotApplicableError{Reason: "coupon " + c.Code + " does not apply to these products"}
			}
		}
	}

	switch c.Kind {
	case KindPercent:
		discount = percentOf(eligible, c.Value)
	case KindFixed:
		discount = min(c.Value, eligible)
	default:
		return 0, fmt.Errorf("coupon %s has unknown kind %q", c.Code, c.Kind)
	}
	return min(discount, subtotal), nil
}

// skuSet is the set of SKUs a coupon is limited to.
type skuSet struct {
	m map[string]bool
}

func (s *skuSet) add(sku string) { s.m[sku] = true }

func (s *skuSet) has(sku string) bool { return s.m[sku] }

// percentOf returns pct percent of total, rounded half up to a whole cent.
func percentOf(total, pct int64) int64 {
	return (total*pct + 50) / 100
}
