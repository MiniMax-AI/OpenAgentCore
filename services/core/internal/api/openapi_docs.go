package api

import (
	"net/http"

	agentsapi "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api"
	"github.com/go-chi/chi/v5"
)

// openAPIDocsPage renders the embedded documents with a pinned Swagger UI.
// The page stays read-only: no operation can be submitted, the authorization
// controls are removed so it never collects a credential, and validatorUrl
// is null so the browser contacts no validator service.
const openAPIDocsPage = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>OpenAgentCore API</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.18.2/swagger-ui.css" integrity="sha384-rcbEi6xgdPk0iWkAQzT2F3FeBJXdG+ydrawGlfHAFIZG7wU6aKbQaRewysYpmrlW" crossorigin="anonymous">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5.18.2/swagger-ui-bundle.js" integrity="sha384-NXtFPpN61oWCuN4D42K6Zd5Rt2+uxeIT36R7kpXBuY9tLnZorzrJ4ykpqwJfgjpZ" crossorigin="anonymous"></script>
<script src="https://unpkg.com/swagger-ui-dist@5.18.2/swagger-ui-standalone-preset.js" integrity="sha384-qr68CD0cvHa88PmVu7e1a58Ego4qvKtcvcLdS2a8Mo5zILI01gyIV9jVwJk7X2NU" crossorigin="anonymous"></script>
<script>
SwaggerUIBundle({
  dom_id: "#swagger-ui",
  urls: [
    {url: "/docs/openapi.yaml", name: "Agents API /v1"},
    {url: "/docs/core.openapi.yaml", name: "Core API /core/v1"},
    {url: "/docs/runtime.openapi.yaml", name: "Machine API /api/v1"}
  ],
  supportedSubmitMethods: [],
  validatorUrl: null,
  layout: "StandaloneLayout",
  presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
  plugins: [() => ({components: {authorizeBtn: () => null, authorizeOperationBtn: () => null}})]
});
</script>
</body>
</html>
`

// registerOpenAPIDocsRoutes serves the API reference page and the documents
// it renders without authentication; they are the published contracts.
func registerOpenAPIDocsRoutes(router chi.Router) {
	router.Get("/docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(openAPIDocsPage))
	})
	router.Get("/docs/{document}", func(w http.ResponseWriter, r *http.Request) {
		document, err := agentsapi.OpenAPI.ReadFile(chi.URLParam(r, "document"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(document)
	})
}
