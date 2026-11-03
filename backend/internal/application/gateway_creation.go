package application

import (
	"context"
	"net/http"
	"strings"
)

type gatewayCreationKey struct{}

func gatewayCreation(r *http.Request) bool {
	required, _ := r.Context().Value(gatewayCreationKey{}).(bool)
	return required
}

// Product creation routes require an explicit governed gateway selection. The
// original routes remain available to offline fixtures and historical clients.
// Reuse the same handlers, workers, receipts and validation; never duplicate or
// fall back to the legacy route after an error.
func registerGatewayCreation(mux *http.ServeMux) {
	for _, route := range []struct{ pattern, suffix, target string }{
		{"POST /api/v1/gateway-tasks", "/gateway-tasks", "/tasks"},
		{"POST /api/v1/gateway-tasks/{id}/retry", "", ""},
		{"POST /api/v1/gateway-runtime-quote", "/gateway-runtime-quote", "/runtime-quote"},
		{"POST /api/v1/projects/{id}/gateway-generations", "/gateway-generations", "/generations"},
		{"POST /api/v1/projects/{id}/gateway-storyboard-generations", "/gateway-storyboard-generations", "/storyboard-generations"},
		{"POST /api/v1/projects/{id}/gateway-analysis", "/gateway-analysis", "/analysis"},
		{"POST /api/v1/projects/{id}/gateway-creative-jobs", "/gateway-creative-jobs", "/creative-jobs"},
	} {
		mux.HandleFunc(route.pattern, func(w http.ResponseWriter, r *http.Request) {
			next := r.Clone(context.WithValue(r.Context(), gatewayCreationKey{}, true))
			if route.suffix == "" {
				next.URL.Path = strings.Replace(r.URL.Path, "/gateway-tasks/", "/tasks/", 1)
			} else {
				next.URL.Path = strings.TrimSuffix(r.URL.Path, route.suffix) + route.target
			}
			next.URL.RawPath = ""
			mux.ServeHTTP(w, next)
		})
	}
}
