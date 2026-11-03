package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/rustambekabdusalomov/shop-api/api"
	"github.com/rustambekabdusalomov/shop-api/internal/apptest"
)

func TestHealthzFollowsTheDatabase(t *testing.T) {
	app := apptest.New(t)

	if rec := app.Do(http.MethodGet, "/healthz", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("database up: status %d, body %s", rec.Code, rec.Body)
	}
	app.Pool.Close()
	rec := app.Do(http.MethodGet, "/healthz", "", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("database closed: status %d, want 503; body %s", rec.Code, rec.Body)
	}
}

func TestServesTheSpecAndSwaggerUI(t *testing.T) {
	app := apptest.New(t)

	rec := app.Do(http.MethodGet, "/api/openapi.yaml", "", "")
	if rec.Code != http.StatusOK || rec.Body.String() != string(api.Spec) {
		t.Fatalf("spec: status %d, body differs from the embedded spec: %v", rec.Code, rec.Body.String() != string(api.Spec))
	}
	if !strings.Contains(rec.Body.String(), "openapi: 3.1.0") {
		t.Error("the served spec is not an OpenAPI 3.1 document")
	}

	rec = app.Do(http.MethodGet, "/api/docs", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "swagger-ui") || !strings.Contains(rec.Body.String(), "/api/openapi.yaml") {
		t.Fatalf("docs: status %d, body %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("docs Content-Type = %q, want text/html", ct)
	}
}
