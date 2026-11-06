package api

import (
	_ "embed"
	"net/http"
)

// OpenAPISpec is the API description; the Expo client's TypeScript types are generated from it.
//
//go:embed openapi.yaml
var OpenAPISpec []byte

func serveOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write(OpenAPISpec)
}
