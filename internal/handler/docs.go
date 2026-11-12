package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// swaggerUIVersion pins the swagger-ui-dist release loaded from the CDN.
const swaggerUIVersion = "5.17.14"

const swaggerUICDN = "https://unpkg.com/swagger-ui-dist@" + swaggerUIVersion

// docsCSP relaxes the API-wide CSP for the docs page only: Swagger UI's assets come from
// the CDN and the page fetches the spec from this origin. Scripts stay external (no inline).
const docsCSP = "default-src 'none'; script-src 'self' https://unpkg.com; style-src https://unpkg.com; " +
	"img-src 'self' data: https://unpkg.com; connect-src 'self'; frame-ancestors 'none'"

const docsHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>ATLAS API Docs</title>
  <link rel="stylesheet" href="` + swaggerUICDN + `/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="` + swaggerUICDN + `/swagger-ui-bundle.js"></script>
  <script src="/docs/init.js"></script>
</body>
</html>
`

const docsInitJS = `window.ui = SwaggerUIBundle({
  url: "/docs/openapi.yaml",
  dom_id: "#swagger-ui",
  deepLinking: true,
  persistAuthorization: true,
  tryItOutEnabled: true
});
`

// DocsHandler serves the OpenAPI contract and a Swagger UI page.
type DocsHandler struct {
	spec []byte
}

// NewDocsHandler returns a DocsHandler serving spec (an OpenAPI YAML document).
func NewDocsHandler(spec []byte) *DocsHandler { return &DocsHandler{spec: spec} }

// UI handles GET /docs.
func (h *DocsHandler) UI(c *gin.Context) {
	c.Header("Content-Security-Policy", docsCSP)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(docsHTML))
}

// InitJS handles GET /docs/init.js.
func (h *DocsHandler) InitJS(c *gin.Context) {
	c.Data(http.StatusOK, "text/javascript; charset=utf-8", []byte(docsInitJS))
}

// Spec handles GET /docs/openapi.yaml.
func (h *DocsHandler) Spec(c *gin.Context) {
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", h.spec)
}
