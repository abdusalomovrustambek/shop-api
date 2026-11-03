// Package apptest runs the whole shop-api handler against a fresh test database and a fake
// auth-gateway. Tests of the feature packages use it to send real HTTP requests.
package apptest

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rustambekabdusalomov/shop-api/internal/auth"
	"github.com/rustambekabdusalomov/shop-api/internal/auth/authtest"
	"github.com/rustambekabdusalomov/shop-api/internal/server"
	"github.com/rustambekabdusalomov/shop-api/internal/testdb"
)

// SyncBuffer is a bytes.Buffer that tests and handlers can share across goroutines.
type SyncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write implements io.Writer.
func (b *SyncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns everything written so far.
func (b *SyncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// App is the handler under test and what it runs on.
type App struct {
	Handler http.Handler
	Pool    *pgxpool.Pool
	Issuer  *authtest.Issuer
	// Logs receives the JSON log at debug level, Stderr the panic reports.
	Logs   *SyncBuffer
	Stderr *SyncBuffer
}

// New starts the app on a fresh database. The test fails when the database is not configured.
func New(t testing.TB) *App {
	t.Helper()
	pool := testdb.New(t)
	iss := authtest.NewIssuer(t)
	logs, stderr := &SyncBuffer{}, &SyncBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := server.New(server.Deps{
		Pool:     pool,
		Verifier: auth.NewVerifier(iss.URL(), log),
		Log:      log,
		Stderr:   stderr,
	})
	return &App{Handler: h, Pool: pool, Issuer: iss, Logs: logs, Stderr: stderr}
}

// Token returns a valid bearer token for the user, a UUID.
func (a *App) Token(t testing.TB, user string) string {
	t.Helper()
	return a.Issuer.UserToken(t, user)
}

// Do sends a request through the handler. token and body may be empty.
func (a *App) Do(method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, req)
	return rec
}

// Decode parses the recorded JSON body into T.
func Decode[T any](t testing.TB, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return v
}
