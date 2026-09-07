package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// newRouter wraps the authenticated MCP handler in a small chi router so the
// binary is deployable behind Kubernetes probes:
//
//   - GET /healthz answers 200 {"status":"ok"} WITHOUT authentication, so the
//     liveness/readiness probes never need a bearer key. It exposes nothing
//     about the service beyond "the process is serving".
//   - The MCP Streamable HTTP endpoint is mounted at BOTH "/" and "/mcp":
//     "/" keeps the original root mount working, "/mcp" matches the
//     warehouse-ops-agent `*_MCP_ENDPOINT` convention and the docs' examples.
//     Every request to either path still goes through the bearer check.
//
// Anything else is a 404 — the router is deliberately not a catch-all, so an
// unauthenticated probe of a random path does not reach the MCP handler.
func newRouter(mcpHandler http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", healthz)
	r.Handle("/", mcpHandler)
	r.Handle("/mcp", mcpHandler)
	return r
}

// healthz is the unauthenticated liveness/readiness endpoint. It reports only
// that the HTTP server is up; the MCP tools' own dependencies (Postgres, the
// reports service) are checked lazily per call.
func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
