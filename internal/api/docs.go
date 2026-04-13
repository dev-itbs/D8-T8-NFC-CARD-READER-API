// Package api provides the HTTP API layer for the LTO NFC Card Reader.
//
// To regenerate swagger.json after adding or modifying handlers, run:
//
//	go generate ./internal/api/...
//
//go:generate swag init -g ../../../cmd/server/main.go -d ../../internal/api/handlers,../../internal/models,../../cmd/server --output . --outputTypes json
package api

import (
	_ "embed"
	"net/http"
)

//go:embed swagger.json
var openapiSpec []byte

func docsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(swaggerUIHTML))
}

func openapiHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openapiSpec)
}

const swaggerUIHTML = `<!DOCTYPE html>
<html>
  <head>
    <title>LTO NFC Card Reader API</title>
    <meta charset="utf-8"/>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <link rel="stylesheet" type="text/css" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-standalone-preset.js"></script>
    <script>
      window.onload = function () {
        SwaggerUIBundle({
          url: "/api/docs/openapi.json",
          dom_id: "#swagger-ui",
          deepLinking: true,
          presets: [
            SwaggerUIBundle.presets.apis,
            SwaggerUIStandalonePreset
          ],
          plugins: [SwaggerUIBundle.plugins.DownloadUrl],
          layout: "StandaloneLayout"
        });
      };
    </script>
  </body>
</html>`
