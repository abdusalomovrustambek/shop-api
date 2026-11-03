package auth

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rustambekabdusalomov/shop-api/internal/auth/authtest"
)

const testUser = "11111111-2222-3333-4444-555555555555"

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

type fixture struct {
	iss   *authtest.Issuer
	v     *Verifier
	clock *fakeClock
	logs  *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	logs := &bytes.Buffer{}
	iss := authtest.NewIssuer(t)
	v := NewVerifier(iss.URL(), slog.New(slog.NewTextHandler(logs, nil)))
	clock := &fakeClock{t: time.Now()}
	v.now = clock.Now
	return &fixture{iss: iss, v: v, clock: clock, logs: logs}
}

// token signs a token for testUser with the given key; exp is relative to the fake clock.
func (f *fixture) token(t *testing.T, signKID, headerKID, alg string, expIn time.Duration) string {
	t.Helper()
	header := map[string]any{"alg": alg, "typ": "JWT"}
	if headerKID != "" {
		header["kid"] = headerKID
	}
	return f.iss.Sign(t, signKID, header, map[string]any{
		"sub": testUser,
		"exp": f.clock.Now().Add(expIn).Unix(),
	})
}

func (f *fixture) good(t *testing.T) string {
	t.Helper()
	return f.token(t, authtest.DefaultKID, authtest.DefaultKID, "EdDSA", time.Hour)
}

// userHandler writes the user id found in the context.
var userHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	id, ok := UserID(r.Context())
	if !ok {
		id = "anonymous"
	}
	if _, err := w.Write([]byte(id)); err != nil {
		panic(err)
	}
})

func serve(h http.Handler, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRequireValidToken(t *testing.T) {
	f := newFixture(t)
	rec := serve(f.v.Require(userHandler), f.good(t))
	if rec.Code != http.StatusOK || rec.Body.String() != testUser {
		t.Fatalf("got %d %q, want 200 %q", rec.Code, rec.Body.String(), testUser)
	}
}

func TestRequireRejects(t *testing.T) {
	f := newFixture(t)
	f.iss.AddKey(t, "evil", false)
	nbf := f.iss.Sign(t, authtest.DefaultKID,
		map[string]any{"alg": "EdDSA", "kid": authtest.DefaultKID},
		map[string]any{"sub": testUser, "exp": f.clock.Now().Add(2 * time.Hour).Unix(), "nbf": f.clock.Now().Add(time.Hour).Unix()})
	badSub := f.iss.Sign(t, authtest.DefaultKID,
		map[string]any{"alg": "EdDSA", "kid": authtest.DefaultKID},
		map[string]any{"sub": "not-a-uuid", "exp": f.clock.Now().Add(time.Hour).Unix()})
	noExp := f.iss.Sign(t, authtest.DefaultKID,
		map[string]any{"alg": "EdDSA", "kid": authtest.DefaultKID},
		map[string]any{"sub": testUser})

	tests := []struct {
		name  string
		token string
	}{
		{"no token", ""},
		{"not a jwt", "abc"},
		{"expired", f.token(t, authtest.DefaultKID, authtest.DefaultKID, "EdDSA", -time.Minute)},
		{"signed by another key", f.token(t, "evil", authtest.DefaultKID, "EdDSA", time.Hour)},
		{"wrong alg", f.token(t, authtest.DefaultKID, authtest.DefaultKID, "HS256", time.Hour)},
		{"alg none", f.token(t, authtest.DefaultKID, authtest.DefaultKID, "none", time.Hour)},
		{"unknown kid", f.token(t, authtest.DefaultKID, "other", "EdDSA", time.Hour)},
		{"missing kid", f.token(t, authtest.DefaultKID, "", "EdDSA", time.Hour)},
		{"not yet valid", nbf},
		{"sub is not a UUID", badSub},
		{"no exp", noExp},
	}
	h := f.v.Require(userHandler)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(h, tt.token)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body %s", rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), `"code":"unauthorized"`) {
				t.Errorf("body %s lacks the unauthorized error code", rec.Body)
			}
			if rec.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("WWW-Authenticate = %q, want Bearer", rec.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestKeysAreFetchedOnceWithinTTL(t *testing.T) {
	f := newFixture(t)
	h := f.v.Require(userHandler)
	for i := 0; i < 3; i++ {
		if rec := serve(h, f.good(t)); rec.Code != http.StatusOK {
			t.Fatalf("request %d: status %d", i, rec.Code)
		}
		f.clock.Advance(4 * time.Minute)
	}
	if n := f.iss.Fetches(); n != 1 {
		t.Fatalf("JWKS fetched %d times within 10 minutes, want 1", n)
	}

	f.clock.Advance(10 * time.Minute)
	if rec := serve(h, f.good(t)); rec.Code != http.StatusOK {
		t.Fatalf("after the TTL: status %d", rec.Code)
	}
	if n := f.iss.Fetches(); n != 2 {
		t.Fatalf("JWKS fetched %d times after the TTL, want 2", n)
	}
}

func TestUnknownKidTriggersOneRefetch(t *testing.T) {
	f := newFixture(t)
	h := f.v.Require(userHandler)
	if rec := serve(h, f.good(t)); rec.Code != http.StatusOK {
		t.Fatalf("first request: status %d", rec.Code)
	}

	// The gateway rotates: k2 is published after the first fetch.
	f.iss.AddKey(t, "k2", true)
	f.clock.Advance(10 * time.Second)
	rotated := f.token(t, "k2", "k2", "EdDSA", time.Hour)
	if rec := serve(h, rotated); rec.Code != http.StatusOK {
		t.Fatalf("token of the new key: status %d, body %s", rec.Code, rec.Body)
	}
	if n := f.iss.Fetches(); n != 2 {
		t.Fatalf("JWKS fetched %d times, want 2 (one refetch)", n)
	}

	// A kid nobody publishes is refused without hammering the endpoint.
	for i := 0; i < 3; i++ {
		if rec := serve(h, f.token(t, authtest.DefaultKID, "ghost", "EdDSA", time.Hour)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("unknown kid: status %d, want 401", rec.Code)
		}
	}
	if n := f.iss.Fetches(); n != 2 {
		t.Fatalf("JWKS fetched %d times right after a refetch, want still 2", n)
	}
	f.clock.Advance(10 * time.Second)
	if rec := serve(h, f.token(t, authtest.DefaultKID, "ghost", "EdDSA", time.Hour)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown kid: status %d, want 401", rec.Code)
	}
	if n := f.iss.Fetches(); n != 3 {
		t.Fatalf("JWKS fetched %d times, want 3 (one more refetch for the later unknown kid)", n)
	}
}

func TestCachedKeysSurviveJWKSOutage(t *testing.T) {
	f := newFixture(t)
	h := f.v.Require(userHandler)
	if rec := serve(h, f.good(t)); rec.Code != http.StatusOK {
		t.Fatalf("first request: status %d", rec.Code)
	}

	f.iss.SetDown(true)
	f.clock.Advance(30 * time.Minute) // the cache is stale now
	if rec := serve(h, f.good(t)); rec.Code != http.StatusOK {
		t.Fatalf("stale cached key during an outage: status %d, body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(f.logs.String(), "fetch jwks") {
		t.Errorf("the failed refresh was not logged: %q", f.logs.String())
	}
}

func TestNothingCachedAndJWKSDown(t *testing.T) {
	f := newFixture(t)
	f.iss.SetDown(true)
	h := f.v.Require(userHandler)

	rec := serve(h, f.good(t))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(f.logs.String(), "level=ERROR") || !strings.Contains(f.logs.String(), "fetch jwks") {
		t.Errorf("want an error log line about the fetch, got %q", f.logs.String())
	}

	// The endpoint comes back; the next attempt after the back-off works.
	f.iss.SetDown(false)
	f.clock.Advance(10 * time.Second)
	if rec := serve(h, f.good(t)); rec.Code != http.StatusOK {
		t.Fatalf("after recovery: status %d, body %s", rec.Code, rec.Body)
	}
}

func TestOptional(t *testing.T) {
	f := newFixture(t)
	h := f.v.Optional(userHandler)

	if rec := serve(h, ""); rec.Code != http.StatusOK || rec.Body.String() != "anonymous" {
		t.Errorf("no token: got %d %q, want 200 anonymous", rec.Code, rec.Body.String())
	}
	if rec := serve(h, f.good(t)); rec.Code != http.StatusOK || rec.Body.String() != testUser {
		t.Errorf("valid token: got %d %q, want 200 %q", rec.Code, rec.Body.String(), testUser)
	}
	bad := f.token(t, authtest.DefaultKID, authtest.DefaultKID, "EdDSA", -time.Minute)
	if rec := serve(h, bad); rec.Code != http.StatusUnauthorized {
		t.Errorf("expired token: status %d, want 401", rec.Code)
	}
}
