// Package server wires the feature packages into one router, so the binary and the
// tests build the same routes.
package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rustambekabdusalomov/shop-api/api"
	"github.com/rustambekabdusalomov/shop-api/internal/auth"
	"github.com/rustambekabdusalomov/shop-api/internal/cart"
	"github.com/rustambekabdusalomov/shop-api/internal/catalog"
	"github.com/rustambekabdusalomov/shop-api/internal/coupon"
	"github.com/rustambekabdusalomov/shop-api/internal/httpx"
	"github.com/rustambekabdusalomov/shop-api/internal/order"
)

// Deps are the things the routes need. Pool and Verifier may be nil when only the route
// table is wanted.
type Deps struct {
	Pool     *pgxpool.Pool
	Verifier *auth.Verifier
	Log      *slog.Logger
	// Stderr receives panic reports; os.Stderr in the binary.
	Stderr io.Writer
}

// Router registers every route.
func Router(d Deps) *httpx.Router {
	var required, optional httpx.Middleware
	if d.Verifier != nil {
		required, optional = d.Verifier.Require, d.Verifier.Optional
	}
	rt := httpx.NewRouter(required, optional)

	couponRepo := coupon.NewRepo(d.Pool)
	catalog.NewHandler(catalog.NewRepo(d.Pool), d.Log).Register(rt)
	cart.NewHandler(cart.NewRepo(d.Pool), couponRepo, d.Log).Register(rt)
	order.NewHandler(order.NewRepo(d.Pool, couponRepo, d.Log), d.Log).Register(rt)
	httpx.RegisterDocs(rt, api.Spec)
	rt.Handle(http.MethodGet, "/healthz", healthz(d.Pool), httpx.AuthNone)
	return rt
}

// New returns the full handler: the router inside the panic recovery and the request log.
func New(d Deps) http.Handler {
	return httpx.Chain(Router(d), httpx.Logger(d.Log), httpx.Recover(d.Stderr))
}

// healthz answers 200 when the database answers a ping, 503 otherwise.
func healthz(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			httpx.WriteError(w, httpx.Errorf(httpx.ErrUnavailable, "database is unreachable"))
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
