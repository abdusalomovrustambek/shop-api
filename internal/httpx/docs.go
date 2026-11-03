package httpx

import (
	"log/slog"
	"net/http"
)

const docsPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Shop API</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({url: "/api/openapi.yaml", dom_id: "#swagger-ui"});
  </script>
</body>
</html>
`

// RegisterDocs serves spec at /api/openapi.yaml and a Swagger UI page at /api/docs.
// The page loads swagger-ui-dist from a CDN; nothing is vendored.
func RegisterDocs(rt *Router, spec []byte) {
	rt.Handle(http.MethodGet, "/api/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		if _, err := w.Write(spec); err != nil {
			slog.Debug("write spec", "err", err)
		}
	}, AuthNone)
	rt.Handle(http.MethodGet, "/api/docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write([]byte(docsPage)); err != nil {
			slog.Debug("write docs page", "err", err)
		}
	}, AuthNone)
}
