package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/rustambekabdusalomov/shop-api/internal/httpx"
)

type userKey struct{}

var errNoToken = errors.New("missing bearer token")

// UserID returns the authenticated user's id (the token's sub) stored by Require or Optional.
func UserID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userKey{}).(string)
	return id, ok && id != ""
}

// Require lets a request through only with a valid bearer token. Without one it
// answers 401, or 503 when no signing keys can be loaded at all.
func (v *Verifier) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			reject(w, errNoToken)
			return
		}
		id, err := v.Verify(r.Context(), token)
		if err != nil {
			reject(w, err)
			return
		}
		next.ServeHTTP(w, withUser(r, id))
	})
}

// Optional lets a request without a token through anonymously. A token that is present
// must still be valid.
func (v *Verifier) Optional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		id, err := v.Verify(r.Context(), token)
		if err != nil {
			reject(w, err)
			return
		}
		next.ServeHTTP(w, withUser(r, id))
	})
}

func withUser(r *http.Request, id string) *http.Request {
	httpx.SetUserID(r.Context(), id)
	return r.WithContext(context.WithValue(r.Context(), userKey{}, id))
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

func reject(w http.ResponseWriter, err error) {
	if errors.Is(err, errKeysUnavailable) {
		httpx.WriteError(w, httpx.Errorf(httpx.ErrUnavailable, "authentication is temporarily unavailable"))
		return
	}
	w.Header().Set("WWW-Authenticate", "Bearer")
	httpx.WriteError(w, httpx.Errorf(httpx.ErrUnauthorized, "missing or invalid bearer token"))
}
