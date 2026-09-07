package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	inboundmcp "github.com/claudioed/fulfillment-execution/internal/adapters/inbound/mcp"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
)

// testRouter builds the real router over the real authenticated MCP handler
// (in-memory adapters, one read key), exactly as run() wires it.
func testRouter(t *testing.T) http.Handler {
	t.Helper()
	tasks := memory.NewTaskRepo()
	server := inboundmcp.NewServer(inboundmcp.Deps{
		GetQueueDepth: &usecases.GetQueueDepth{Tasks: tasks},
		Tasks:         tasks,
	})
	auth := inboundmcp.NewStaticKeyAuth(map[string]inboundmcp.Scope{"read-key": inboundmcp.ScopeRead})
	return newRouter(inboundmcp.Handler(server, auth))
}

func TestHealthzIsUnauthenticated(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz without a bearer key: got %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"status":"ok"}` {
		t.Fatalf("body = %q, want {\"status\":\"ok\"}", body)
	}
}

func TestMCPMountsRequireBearer(t *testing.T) {
	for _, path := range []string{"/", "/mcp"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
			req.Header.Set("Content-Type", "application/json")
			testRouter(t).ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("POST %s without a bearer key: got %d, want 401", path, rec.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Fatalf("WWW-Authenticate = %q, want a Bearer challenge from the MCP auth middleware", got)
			}
		})
	}
}

func TestMCPMountsReachStreamableHandlerWithBearer(t *testing.T) {
	for _, path := range []string{"/", "/mcp"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("Authorization", "Bearer read-key")
			testRouter(t).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("POST %s initialize with a valid key: got %d, want 200 (body %q)", path, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "fulfillment-execution-mcp") {
				t.Fatalf("initialize response does not name the server: %q", rec.Body.String())
			}
		})
	}
}

func TestUnknownPathIsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /nope: got %d, want 404", rec.Code)
	}
}
