package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouterWrapsRoutesByAuthMode(t *testing.T) {
	mark := func(header string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set(header, "yes")
				next.ServeHTTP(w, r)
			})
		}
	}
	rt := NewRouter(mark("X-Required"), mark("X-Optional"))
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	rt.Handle(http.MethodGet, "/none", ok, AuthNone)
	rt.Handle(http.MethodGet, "/required", ok, AuthRequired)
	rt.Handle(http.MethodGet, "/optional/{id}", ok, AuthOptional)

	tests := []struct {
		path, wantRequired, wantOptional string
	}{
		{"/none", "", ""},
		{"/required", "yes", ""},
		{"/optional/7", "", "yes"},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s: status %d", tt.path, rec.Code)
		}
		if got := rec.Header().Get("X-Required"); got != tt.wantRequired {
			t.Errorf("%s: X-Required = %q, want %q", tt.path, got, tt.wantRequired)
		}
		if got := rec.Header().Get("X-Optional"); got != tt.wantOptional {
			t.Errorf("%s: X-Optional = %q, want %q", tt.path, got, tt.wantOptional)
		}
	}

	routes := rt.Routes()
	if len(routes) != 3 || routes[1] != (Route{Method: "GET", Pattern: "/required", Auth: AuthRequired}) {
		t.Errorf("Routes() = %+v", routes)
	}

	rec := httptest.NewRecorder()
	rt.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/none", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST to a GET route: status %d, want 405", rec.Code)
	}
}
