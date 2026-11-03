// Package order holds checkout, orders and the billing job that follows an order.
package order

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rustambekabdusalomov/shop-api/internal/coupon"
	"github.com/rustambekabdusalomov/shop-api/internal/httpx"
)

// listLimit is the most orders GET /api/orders returns.
const listLimit = 100

// chargeOrderKind is the billing.jobs kind billing-worker picks up.
const chargeOrderKind = "charge_order"

// Card is the card data the buyer sends. It goes into the billing job and nowhere else.
type Card struct {
	Number string `json:"number"`
	Exp    string `json:"exp"`
	CVC    string `json:"cvc"`
}

// CheckoutInput is what checkout needs from the request. A non-empty CouponCode wins
// over the coupon stored on the cart.
type CheckoutInput struct {
	UserID     string
	Currency   string
	Card       Card
	CouponCode string
}

// Item is a line of an order, a snapshot of the product at checkout.
type Item struct {
	ProductID      int64  `json:"product_id"`
	Name           string `json:"name"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	Quantity       int    `json:"quantity"`
}

// Order is an order with its items; all amounts are integer cents.
type Order struct {
	ID            int64     `json:"id"`
	Status        string    `json:"status"`
	Currency      string    `json:"currency"`
	SubtotalCents int64     `json:"subtotal_cents"`
	DiscountCents int64     `json:"discount_cents"`
	TotalCents    int64     `json:"total_cents"`
	CouponCode    *string   `json:"coupon_code"`
	CreatedAt     time.Time `json:"created_at"`
	Items         []Item    `json:"items"`
}

// jobPayload is the payload of a billing.jobs row of kind charge_order.
type jobPayload struct {
	OrderID    int64  `json:"order_id"`
	UserID     string `json:"user_id"`
	Currency   string `json:"currency"`
	TotalCents int64  `json:"total_cents"`
	Card       Card   `json:"card"`
}

// line is a cart line joined with its locked product row.
type line struct {
	ProductID      int64
	SKU            string
	Name           string
	UnitPriceCents int64
	Stock          int
	Quantity       int
}

// Repo reads and writes orders.
type Repo struct {
	db      *pgxpool.Pool
	coupons *coupon.Repo
	log     *slog.Logger
	now     func() time.Time
}

// NewRepo returns a Repo on db.
func NewRepo(db *pgxpool.Pool, coupons *coupon.Repo, log *slog.Logger) *Repo {
	return &Repo{db: db, coupons: coupons, log: log, now: time.Now}
}

// Checkout turns the user's cart into an order in one transaction: it locks the products,
// checks stock, applies the coupon, writes the order and its items, decrements stock,
// enqueues the charge_order job and empties the cart. Any failure writes nothing.
func (r *Repo) Checkout(ctx context.Context, in CheckoutInput) (Order, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return Order{}, fmt.Errorf("begin checkout: %w", err)
	}
	defer r.rollback(ctx, tx)

	var cartID int64
	var cartCoupon *string
	err = tx.QueryRow(ctx,
		`SELECT id, coupon_code FROM shop.carts WHERE user_id = $1 FOR UPDATE`, in.UserID,
	).Scan(&cartID, &cartCoupon)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, httpx.Errorf(httpx.ErrInvalid, "cart is empty")
	}
	if err != nil {
		return Order{}, fmt.Errorf("lock cart: %w", err)
	}

	lines, err := lockLines(ctx, tx, cartID)
	if err != nil {
		return Order{}, err
	}
	if len(lines) == 0 {
		return Order{}, httpx.Errorf(httpx.ErrInvalid, "cart is empty")
	}
	couponLines := make([]coupon.Line, len(lines))
	for i, l := range lines {
		if l.Quantity > l.Stock {
			return Order{}, httpx.Errorf(httpx.ErrConflict, "not enough stock for %s", l.Name)
		}
		couponLines[i] = coupon.Line{SKU: l.SKU, Quantity: l.Quantity, UnitPriceCents: l.UnitPriceCents}
	}

	code := in.CouponCode
	if code == "" && cartCoupon != nil {
		code = *cartCoupon
	}
	subtotal := coupon.Subtotal(couponLines)
	var discount int64
	var appliedCode *string
	if code != "" {
		cp, err := r.coupons.ByCode(ctx, code)
		if err != nil {
			return Order{}, err
		}
		if discount, err = coupon.Discount(cp, couponLines, r.now()); err != nil {
			return Order{}, err
		}
		appliedCode = &cp.Code
	}

	o := Order{
		Status:        "pending",
		Currency:      in.Currency,
		SubtotalCents: subtotal,
		DiscountCents: discount,
		TotalCents:    subtotal - discount,
		CouponCode:    appliedCode,
		Items:         make([]Item, 0, len(lines)),
	}
	err = tx.QueryRow(ctx,
		`INSERT INTO shop.orders (user_id, currency, subtotal_cents, discount_cents, total_cents, coupon_code)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at`,
		in.UserID, o.Currency, o.SubtotalCents, o.DiscountCents, o.TotalCents, o.CouponCode,
	).Scan(&o.ID, &o.CreatedAt)
	if err != nil {
		return Order{}, fmt.Errorf("insert order: %w", err)
	}

	for _, l := range lines {
		_, err = tx.Exec(ctx,
			`INSERT INTO shop.order_items (order_id, product_id, name, unit_price_cents, quantity)
			 VALUES ($1, $2, $3, $4, $5)`,
			o.ID, l.ProductID, l.Name, l.UnitPriceCents, l.Quantity)
		if err != nil {
			return Order{}, fmt.Errorf("insert order item: %w", err)
		}
		_, err = tx.Exec(ctx,
			`UPDATE shop.products SET stock = stock - $1 WHERE id = $2`, l.Quantity, l.ProductID)
		if err != nil {
			return Order{}, fmt.Errorf("decrement stock: %w", err)
		}
		o.Items = append(o.Items, Item{ProductID: l.ProductID, Name: l.Name, UnitPriceCents: l.UnitPriceCents, Quantity: l.Quantity})
	}

	payload := jobPayload{OrderID: o.ID, UserID: in.UserID, Currency: o.Currency, TotalCents: o.TotalCents, Card: in.Card}
	if _, err = tx.Exec(ctx,
		`INSERT INTO billing.jobs (kind, payload) VALUES ($1, $2)`, chargeOrderKind, payload); err != nil {
		return Order{}, fmt.Errorf("enqueue %s job: %w", chargeOrderKind, err)
	}

	if _, err = tx.Exec(ctx, `DELETE FROM shop.cart_items WHERE cart_id = $1`, cartID); err != nil {
		return Order{}, fmt.Errorf("empty cart: %w", err)
	}
	if _, err = tx.Exec(ctx,
		`UPDATE shop.carts SET coupon_code = NULL, updated_at = now() WHERE id = $1`, cartID); err != nil {
		return Order{}, fmt.Errorf("clear cart coupon: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Order{}, fmt.Errorf("commit checkout: %w", err)
	}
	return o, nil
}

// lockLines reads the cart lines and locks their product rows in id order, so concurrent
// checkouts queue up instead of overselling or deadlocking.
func lockLines(ctx context.Context, tx pgx.Tx, cartID int64) ([]line, error) {
	rows, err := tx.Query(ctx,
		`SELECT p.id, p.sku, p.name, p.price_cents, p.stock, ci.quantity
		   FROM shop.cart_items ci
		   JOIN shop.products p ON p.id = ci.product_id
		  WHERE ci.cart_id = $1
		  ORDER BY p.id
		    FOR UPDATE OF p`, cartID)
	if err != nil {
		return nil, fmt.Errorf("lock cart products: %w", err)
	}
	defer rows.Close()

	var lines []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.ProductID, &l.SKU, &l.Name, &l.UnitPriceCents, &l.Stock, &l.Quantity); err != nil {
			return nil, fmt.Errorf("scan cart line: %w", err)
		}
		lines = append(lines, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read cart lines: %w", err)
	}
	return lines, nil
}

// rollback undoes tx unless it is already committed or closed.
func (r *Repo) rollback(ctx context.Context, tx pgx.Tx) {
	err := tx.Rollback(context.WithoutCancel(ctx))
	if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		r.log.Error("rollback checkout", "err", err)
	}
}

const orderColumns = `id, status, currency, subtotal_cents, discount_cents, total_cents, coupon_code, created_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanOrder(s scanner) (Order, error) {
	o := Order{Items: []Item{}}
	err := s.Scan(&o.ID, &o.Status, &o.Currency, &o.SubtotalCents, &o.DiscountCents, &o.TotalCents, &o.CouponCode, &o.CreatedAt)
	return o, err
}

// List returns the user's newest orders first.
func (r *Repo) List(ctx context.Context, userID string) ([]Order, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+orderColumns+` FROM shop.orders
		  WHERE user_id = $1
		  ORDER BY created_at DESC, id DESC
		  LIMIT $2`, userID, listLimit)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()

	orders := []Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read orders: %w", err)
	}
	rows.Close()

	if err := r.attachItems(ctx, orders); err != nil {
		return nil, err
	}
	return orders, nil
}

// Get returns one of the user's orders; someone else's order is ErrNotFound.
func (r *Repo) Get(ctx context.Context, userID string, id int64) (Order, error) {
	o, err := scanOrder(r.db.QueryRow(ctx,
		`SELECT `+orderColumns+` FROM shop.orders WHERE id = $1 AND user_id = $2`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, httpx.Errorf(httpx.ErrNotFound, "order %d not found", id)
	}
	if err != nil {
		return Order{}, fmt.Errorf("load order: %w", err)
	}
	orders := []Order{o}
	if err := r.attachItems(ctx, orders); err != nil {
		return Order{}, err
	}
	return orders[0], nil
}

// attachItems loads the items of orders and sets them on each order.
func (r *Repo) attachItems(ctx context.Context, orders []Order) error {
	if len(orders) == 0 {
		return nil
	}
	ids := make([]int64, len(orders))
	position := make(map[int64]int, len(orders))
	for i, o := range orders {
		ids[i] = o.ID
		position[o.ID] = i
	}
	rows, err := r.db.Query(ctx,
		`SELECT order_id, product_id, name, unit_price_cents, quantity
		   FROM shop.order_items
		  WHERE order_id = ANY($1)
		  ORDER BY order_id, product_id`, ids)
	if err != nil {
		return fmt.Errorf("list order items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var orderID int64
		var it Item
		if err := rows.Scan(&orderID, &it.ProductID, &it.Name, &it.UnitPriceCents, &it.Quantity); err != nil {
			return fmt.Errorf("scan order item: %w", err)
		}
		o := &orders[position[orderID]]
		o.Items = append(o.Items, it)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read order items: %w", err)
	}
	return nil
}
