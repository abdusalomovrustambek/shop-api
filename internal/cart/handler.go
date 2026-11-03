package cart

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rustambekabdusalomov/shop-api/internal/auth"
	"github.com/rustambekabdusalomov/shop-api/internal/coupon"
	"github.com/rustambekabdusalomov/shop-api/internal/httpx"
)

// View is the cart as the API returns it, with totals in cents.
type View struct {
	Items         []Item  `json:"items"`
	CouponCode    *string `json:"coupon_code"`
	SubtotalCents int64   `json:"subtotal_cents"`
	DiscountCents int64   `json:"discount_cents"`
	TotalCents    int64   `json:"total_cents"`
}

// Handler serves the cart routes. Every route needs a token.
type Handler struct {
	repo    *Repo
	coupons *coupon.Repo
	log     *slog.Logger
	now     func() time.Time
}

// NewHandler returns a Handler.
func NewHandler(repo *Repo, coupons *coupon.Repo, log *slog.Logger) *Handler {
	return &Handler{repo: repo, coupons: coupons, log: log, now: time.Now}
}

// Register adds the cart routes to rt.
func (h *Handler) Register(rt *httpx.Router) {
	rt.Handle(http.MethodGet, "/api/cart", h.Get, httpx.AuthRequired)
	rt.Handle(http.MethodPost, "/api/cart/items", h.AddItem, httpx.AuthRequired)
	rt.Handle(http.MethodPatch, "/api/cart/items/{product_id}", h.SetQuantity, httpx.AuthRequired)
	rt.Handle(http.MethodPost, "/api/cart/coupon", h.ApplyCoupon, httpx.AuthRequired)
}

// Get answers GET /api/cart; the cart is created on first use.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	c, ok := h.cart(w, r)
	if !ok {
		return
	}
	h.respond(w, r, http.StatusOK, c)
}

// AddItem answers POST /api/cart/items.
func (h *Handler) AddItem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProductID int64 `json:"product_id"`
		Quantity  int   `json:"quantity"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if in.ProductID < 1 {
		httpx.WriteError(w, httpx.Errorf(httpx.ErrInvalid, "product_id is required"))
		return
	}
	if in.Quantity < 0 || in.Quantity > MaxQuantity {
		httpx.WriteError(w, httpx.Errorf(httpx.ErrInvalid, "quantity must be between 1 and %d", MaxQuantity))
		return
	}
	c, ok := h.cart(w, r)
	if !ok {
		return
	}
	if err := h.repo.AddItem(r.Context(), c.ID, in.ProductID, in.Quantity); err != nil {
		httpx.Fail(w, h.log, "add cart item failed", err, "product_id", in.ProductID, "quantity", in.Quantity)
		return
	}
	h.respond(w, r, http.StatusCreated, c)
}

// SetQuantity answers PATCH /api/cart/items/{product_id}; quantity 0 removes the item.
func (h *Handler) SetQuantity(w http.ResponseWriter, r *http.Request) {
	productID, err := strconv.ParseInt(r.PathValue("product_id"), 10, 64)
	if err != nil || productID < 1 {
		httpx.WriteError(w, httpx.Errorf(httpx.ErrInvalid, "product_id must be a positive integer"))
		return
	}
	var in struct {
		Quantity *int `json:"quantity"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if in.Quantity == nil || *in.Quantity < 0 || *in.Quantity > MaxQuantity {
		httpx.WriteError(w, httpx.Errorf(httpx.ErrInvalid, "quantity must be between 0 and %d", MaxQuantity))
		return
	}
	c, ok := h.cart(w, r)
	if !ok {
		return
	}
	if *in.Quantity == 0 {
		err = h.repo.RemoveItem(r.Context(), c.ID, productID)
	} else {
		err = h.repo.SetQuantity(r.Context(), c.ID, productID, *in.Quantity)
	}
	if err != nil {
		httpx.Fail(w, h.log, "update cart item failed", err, "product_id", productID, "quantity", *in.Quantity)
		return
	}
	h.respond(w, r, http.StatusOK, c)
}

// ApplyCoupon answers POST /api/cart/coupon.
func (h *Handler) ApplyCoupon(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if strings.TrimSpace(in.Code) == "" {
		httpx.WriteError(w, httpx.Errorf(httpx.ErrInvalid, "code is required"))
		return
	}
	c, ok := h.cart(w, r)
	if !ok {
		return
	}
	items, err := h.repo.Items(r.Context(), c.ID)
	if err != nil {
		httpx.Fail(w, h.log, "load cart items failed", err)
		return
	}
	cp, err := h.coupons.ByCode(r.Context(), in.Code)
	if err == nil {
		_, err = coupon.Discount(cp, couponLines(items), h.now())
	}
	if err != nil {
		httpx.Fail(w, h.log, "apply coupon failed", err, "code", in.Code)
		return
	}
	if err := h.repo.SetCoupon(r.Context(), c.ID, &cp.Code); err != nil {
		httpx.Fail(w, h.log, "store coupon failed", err, "code", cp.Code)
		return
	}
	c.CouponCode = &cp.Code
	h.respond(w, r, http.StatusOK, c)
}

// cart loads the caller's cart; on failure it has already written the response.
func (h *Handler) cart(w http.ResponseWriter, r *http.Request) (Cart, bool) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.Errorf(httpx.ErrUnauthorized, "missing or invalid bearer token"))
		return Cart{}, false
	}
	c, err := h.repo.GetOrCreate(r.Context(), userID)
	if err != nil {
		httpx.Fail(w, h.log, "load cart failed", err, "user_id", userID)
		return Cart{}, false
	}
	return c, true
}

func (h *Handler) respond(w http.ResponseWriter, r *http.Request, status int, c Cart) {
	view, err := h.view(r.Context(), c)
	if err != nil {
		httpx.Fail(w, h.log, "build cart view failed", err, "cart_id", c.ID)
		return
	}
	httpx.WriteJSON(w, status, view)
}

func (h *Handler) view(ctx context.Context, c Cart) (View, error) {
	items, err := h.repo.Items(ctx, c.ID)
	if err != nil {
		return View{}, err
	}
	lines := couponLines(items)
	v := View{Items: items, CouponCode: c.CouponCode, SubtotalCents: coupon.Subtotal(lines)}
	if c.CouponCode != nil {
		if v.DiscountCents, err = h.discount(ctx, *c.CouponCode, lines); err != nil {
			return View{}, err
		}
	}
	v.TotalCents = v.SubtotalCents - v.DiscountCents
	return v, nil
}

// discount returns the discount of the coupon on lines, or 0 when it no longer applies.
func (h *Handler) discount(ctx context.Context, code string, lines []coupon.Line) (int64, error) {
	cp, err := h.coupons.ByCode(ctx, code)
	if err == nil {
		var d int64
		if d, err = coupon.Discount(cp, lines, h.now()); err == nil {
			return d, nil
		}
	}
	var na *coupon.NotApplicableError
	if errors.As(err, &na) {
		return 0, nil
	}
	return 0, err
}

func couponLines(items []Item) []coupon.Line {
	lines := make([]coupon.Line, len(items))
	for i, it := range items {
		lines[i] = coupon.Line{SKU: it.SKU, Quantity: it.Quantity, UnitPriceCents: it.UnitPriceCents}
	}
	return lines
}
