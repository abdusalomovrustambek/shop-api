// Package api embeds the OpenAPI spec so the server can serve it.
package api

import _ "embed"

// Spec is the OpenAPI 3.1 document, api/openapi.yaml.
//
//go:embed openapi.yaml
var Spec []byte
