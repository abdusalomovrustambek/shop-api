// Package cart holds the caller's cart: items, quantities and the applied coupon.
package cart

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rustambekabdusalomov/shop-api/internal/httpx"
)

// MaxQuantity is the most units of one product a cart line can hold.
const MaxQuantity = 99

// foreignKeyViolation is the SQLSTATE for a reference to a row that does not exist.
const foreignKeyViolation = "23503"

// Cart is a row of shop.carts.
type Cart struct {
	ID         int64
	CouponCode *string
}

// Item is one cart line with the product's current name and price.
type Item struct {
	ProductID      int64  `json:"product_id"`
	SKU            string `json:"sku"`
	Name           string `json:"name"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	Quantity       int    `json:"quantity"`
	LineTotalCents int64  `json:"line_total_cents"`
}

// Repo reads and writes carts and cart items.
type Repo struct {
	db *pgxpool.Pool
}

// NewRepo returns a Repo on db.
func NewRepo(db *pgxpool.Pool) *Repo {
	return &Repo{db: db}
}

// GetOrCreate returns the user's cart, creating it on first use.
func (r *Repo) GetOrCreate(ctx context.Context, userID string) (Cart, error) {
	var c Cart
	err := r.db.QueryRow(ctx,
		`INSERT INTO shop.carts (user_id) VALUES ($1)
		 ON CONFLICT (user_id) DO UPDATE SET updated_at = now()
		 RETURNING id, coupon_code`, userID,
	).Scan(&c.ID, &c.CouponCode)
	if err != nil {
		return Cart{}, fmt.Errorf("get cart: %w", err)
	}
	return c, nil
}

// Items returns the cart's lines in product order.
func (r *Repo) Items(ctx context.Context, cartID int64) ([]Item, error) {
	rows, err := r.db.Query(ctx,
		`SELECT p.id, p.sku, p.name, p.price_cents, ci.quantity
		   FROM shop.cart_items ci
		   JOIN shop.products p ON p.id = ci.product_id
		  WHERE ci.cart_id = $1
		  ORDER BY p.id`, cartID)
	if err != nil {
		return nil, fmt.Errorf("list cart items: %w", err)
	}
	defer rows.Close()

	items := []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ProductID, &it.SKU, &it.Name, &it.UnitPriceCents, &it.Quantity); err != nil {
			return nil, fmt.Errorf("scan cart item: %w", err)
		}
		it.LineTotalCents = int64(it.Quantity) * it.UnitPriceCents
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read cart items: %w", err)
	}
	return items, nil
}

// AddItem adds quantity units of a product to the cart, up to MaxQuantity in total.
// An unknown product is ErrNotFound.
func (r *Repo) AddItem(ctx context.Context, cartID, productID int64, quantity int) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO shop.cart_items AS ci (cart_id, product_id, quantity)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (cart_id, product_id)
		 DO UPDATE SET quantity = LEAST(ci.quantity + EXCLUDED.quantity, $4)`,
		cartID, productID, quantity, MaxQuantity)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
		return httpx.Errorf(httpx.ErrNotFound, "product %d does not exist", productID)
	}
	if err != nil {
		return fmt.Errorf("add cart item: %w", err)
	}
	return nil
}

// SetQuantity sets the quantity of a product already in the cart. A product that is
// not in the cart is ErrNotFound.
func (r *Repo) SetQuantity(ctx context.Context, cartID, productID int64, quantity int) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE shop.cart_items SET quantity = $3 WHERE cart_id = $1 AND product_id = $2`,
		cartID, productID, quantity)
	if err != nil {
		return fmt.Errorf("set cart item quantity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return httpx.Errorf(httpx.ErrNotFound, "product %d is not in the cart", productID)
	}
	return nil
}

// RemoveItem deletes a product from the cart. A product that is not in the cart is ErrNotFound.
func (r *Repo) RemoveItem(ctx context.Context, cartID, productID int64) error {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM shop.cart_items WHERE cart_id = $1 AND product_id = $2`,
		cartID, productID)
	if err != nil {
		return fmt.Errorf("remove cart item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return httpx.Errorf(httpx.ErrNotFound, "product %d is not in the cart", productID)
	}
	return nil
}

// SetCoupon stores the applied coupon code on the cart; nil clears it.
func (r *Repo) SetCoupon(ctx context.Context, cartID int64, code *string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE shop.carts SET coupon_code = $2, updated_at = now() WHERE id = $1`,
		cartID, code)
	if err != nil {
		return fmt.Errorf("set cart coupon: %w", err)
	}
	return nil
}
