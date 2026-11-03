package httpx

import "net/http"

// AuthMode says how a route treats the bearer token.
type AuthMode int

const (
	// AuthNone routes ignore the token.
	AuthNone AuthMode = iota
	// AuthRequired routes answer 401 without a valid token.
	AuthRequired
	// AuthOptional routes accept requests without a token and run them anonymously.
	AuthOptional
)

// Route is one registered method and path pattern.
type Route struct {
	Method  string
	Pattern string
	Auth    AuthMode
}

// Router registers routes on a http.ServeMux (Go 1.22+ method patterns) and remembers
// them, because ServeMux cannot list its routes.
type Router struct {
	mux      *http.ServeMux
	required Middleware
	optional Middleware
	routes   []Route
}

// NewRouter returns an empty router. required and optional wrap the routes with the
// matching AuthMode; nil means no wrapping.
func NewRouter(required, optional Middleware) *Router {
	return &Router{mux: http.NewServeMux(), required: required, optional: optional}
}

// Handle registers h for method and pattern (for example "/api/products/{id}").
func (rt *Router) Handle(method, pattern string, h http.HandlerFunc, mode AuthMode) {
	var handler http.Handler = h
	switch mode {
	case AuthRequired:
		if rt.required != nil {
			handler = rt.required(handler)
		}
	case AuthOptional:
		if rt.optional != nil {
			handler = rt.optional(handler)
		}
	case AuthNone:
	}
	rt.mux.Handle(method+" "+pattern, handler)
	rt.routes = append(rt.routes, Route{Method: method, Pattern: pattern, Auth: mode})
}

// Routes returns the registered routes in registration order.
func (rt *Router) Routes() []Route {
	return append([]Route(nil), rt.routes...)
}

// ServeHTTP implements http.Handler.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt.mux.ServeHTTP(w, r)
}
