package httpx_test

import (
	"io"
	"log/slog"
	"sort"
	"strings"
	"testing"

	"github.com/rustambekabdusalomov/shop-api/api"
	"github.com/rustambekabdusalomov/shop-api/internal/server"
)

var httpMethods = map[string]bool{"get": true, "put": true, "post": true, "delete": true, "patch": true, "options": true, "head": true}

// specOperations lists "METHOD /path" for every operation under paths: in the spec. The
// spec keeps a fixed layout (paths at two spaces, methods at four), so a line scanner is
// enough and no YAML library is needed.
func specOperations(t *testing.T, spec string) []string {
	t.Helper()
	var ops []string
	inPaths := false
	path := ""
	for _, line := range strings.Split(spec, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 {
			inPaths = trimmed == "paths:"
			continue
		}
		if !inPaths {
			continue
		}
		key, ok := strings.CutSuffix(trimmed, ":")
		if !ok {
			continue
		}
		switch indent {
		case 2:
			path = key
		case 4:
			if httpMethods[key] {
				if path == "" {
					t.Fatalf("method %q before any path", key)
				}
				ops = append(ops, strings.ToUpper(key)+" "+path)
			}
		}
	}
	sort.Strings(ops)
	return ops
}

func TestSpecAndRouterListTheSameOperations(t *testing.T) {
	rt := server.Router(server.Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})

	var routes []string
	for _, r := range rt.Routes() {
		// /healthz is the compose health check, not part of the public API.
		if !strings.HasPrefix(r.Pattern, "/api/") {
			continue
		}
		routes = append(routes, r.Method+" "+r.Pattern)
	}
	sort.Strings(routes)
	spec := specOperations(t, string(api.Spec))

	if len(routes) == 0 || len(spec) == 0 {
		t.Fatalf("found %d routes and %d spec operations, want both non-empty", len(routes), len(spec))
	}
	inSpec := make(map[string]bool, len(spec))
	for _, op := range spec {
		inSpec[op] = true
	}
	inRouter := make(map[string]bool, len(routes))
	for _, op := range routes {
		inRouter[op] = true
		if !inSpec[op] {
			t.Errorf("route %q is registered but not in api/openapi.yaml", op)
		}
	}
	for _, op := range spec {
		if !inRouter[op] {
			t.Errorf("operation %q is in api/openapi.yaml but not registered", op)
		}
	}
}

func TestSpecScanner(t *testing.T) {
	const doc = `openapi: 3.1.0
paths:
  /a:
    get:
      parameters:
        - name: x
    post:
      summary: p
  /b/{id}:
    delete:
      summary: d
components:
  schemas:
    get:
      type: string
`
	got := strings.Join(specOperations(t, doc), ",")
	if want := "DELETE /b/{id},GET /a,POST /a"; got != want {
		t.Errorf("operations = %q, want %q", got, want)
	}
}
