// Package api embeds the OpenAPI contract of the REST API (openapi.yaml), the source of
// truth for the frontend. Update it in the same change as any handler or DTO change.
package api

import _ "embed"

// OpenAPI is the OpenAPI 3 document served at /docs/openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPI []byte
