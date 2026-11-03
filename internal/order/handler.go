package order

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/rustambekabdusalomov/shop-api/internal/auth"
	"github.com/rustambekabdusalomov/shop-api/internal/httpx"
)

// Handler serves checkout and the order routes.
type Handler struct {
	repo *Repo
	log  *slog.Logger
}

// NewHandler returns a Handler.
func NewHandler(repo *Repo, log *slog.Logger) *Handler {
	return &Handler{repo: repo, log: log}
}

// Register adds the order routes to rt.
func (h *Handler) Register(rt *httpx.Router) {
	rt.Handle(http.MethodPost, "/api/checkout", h.Checkout, httpx.AuthRequired)
	rt.Handle(http.MethodGet, "/api/orders", h.List, httpx.AuthOptional)
	rt.Handle(http.MethodGet, "/api/orders/{id}", h.Get, httpx.AuthRequired)
}

type checkoutRequest struct {
	Currency   string `json:"currency"`
	Card       Card   `json:"card"`
	CouponCode string `json:"coupon_code"`
}

// Checkout answers POST /api/checkout.
func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.Errorf(httpx.ErrUnauthorized, "missing or invalid bearer token"))
		return
	}
	var in checkoutRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := validate(in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	o, err := h.repo.Checkout(r.Context(), CheckoutInput{
		UserID:     userID,
		Currency:   in.Currency,
		Card:       in.Card,
		CouponCode: in.CouponCode,
	})
	if err != nil {
		// The card is not a log input: only the user and the currency are.
		httpx.Fail(w, h.log, "checkout failed", err, "user_id", userID, "currency", in.Currency)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, o)
}

// List answers GET /api/orders with the caller's orders, newest first.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, []Order{})
		return
	}
	orders, err := h.repo.List(r.Context(), userID)
	if err != nil {
		httpx.Fail(w, h.log, "list orders failed", err, "user_id", userID)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, orders)
}

// Get answers GET /api/orders/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.Errorf(httpx.ErrUnauthorized, "missing or invalid bearer token"))
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, httpx.Errorf(httpx.ErrInvalid, "order id must be an integer"))
		return
	}
	o, err := h.repo.Get(r.Context(), userID, id)
	if err != nil {
		httpx.Fail(w, h.log, "get order failed", err, "user_id", userID, "order_id", id)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, o)
}

// validate checks the request fields: a currency of three uppercase letters (UZS included)
// and a card with a numeric number of 12 to 19 digits, an expiry and a CVC.
func validate(in checkoutRequest) error {
	if len(in.Currency) != 3 || !isUpperLetters(in.Currency) {
		return httpx.Errorf(httpx.ErrInvalid, "currency must be a 3-letter uppercase code")
	}
	if n := len(in.Card.Number); n < 12 || n > 19 || !isDigits(in.Card.Number) {
		return httpx.Errorf(httpx.ErrInvalid, "card.number must be 12 to 19 digits")
	}
	if in.Card.Exp == "" {
		return httpx.Errorf(httpx.ErrInvalid, "card.exp is required")
	}
	if n := len(in.Card.CVC); n < 3 || n > 4 || !isDigits(in.Card.CVC) {
		return httpx.Errorf(httpx.ErrInvalid, "card.cvc must be 3 or 4 digits")
	}
	return nil
}

func isUpperLetters(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
