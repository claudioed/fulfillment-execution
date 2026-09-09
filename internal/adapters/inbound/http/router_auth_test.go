package http_test

import (
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/claudioed/fulfillment-execution/internal/adapters/inbound/auth"
	"github.com/claudioed/fulfillment-execution/internal/adapters/inbound/http"
	"github.com/claudioed/fulfillment-execution/internal/analytics/report"
)

const (
	testReadKey = "read-key"
	testRWKey   = "rw-key"
)

func testAuthn() auth.Authenticator {
	return auth.NewStaticKeyAuth(map[string]auth.Scope{
		testReadKey: auth.ScopeRead,
		testRWKey:   auth.ScopeReadWrite,
	})
}

func doAuth(t *testing.T, srv stdhttp.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func assertProblem(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantSlug string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("want %d, got %d body=%s", wantStatus, rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("want application/problem+json, got %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("problem body is not JSON: %v", err)
	}
	if body["type"] != http.ProblemBase+wantSlug {
		t.Fatalf("want problem type %q, got %v", http.ProblemBase+wantSlug, body["type"])
	}
	if body["status"] != float64(wantStatus) {
		t.Fatalf("want status %d in body, got %v", wantStatus, body["status"])
	}
}

// TestRouter_Auth_Enforce is the fleet-standard router table (ADR-0021):
// one GET and one mutating route, plus /healthz outside the middleware.
func TestRouter_Auth_Enforce(t *testing.T) {
	h, _, _, _, _ := newTestHandlers()
	srv := http.NewRouter(h, nil, http.WithAuth(testAuthn(), auth.ModeEnforce))

	const stationBody = `{"stationId":"st-auth","capabilities":["pick"]}`

	t.Run("healthz without token is 200", func(t *testing.T) {
		if rec := doAuth(t, srv, stdhttp.MethodGet, "/healthz", "", ""); rec.Code != stdhttp.StatusOK {
			t.Fatalf("want 200, got %d", rec.Code)
		}
	})
	t.Run("GET without token is 401 problem+json with WWW-Authenticate", func(t *testing.T) {
		rec := doAuth(t, srv, stdhttp.MethodGet, "/queues/PICK/depth", "", "")
		assertProblem(t, rec, stdhttp.StatusUnauthorized, "unauthenticated")
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Fatal("401 must carry WWW-Authenticate")
		}
	})
	t.Run("GET with bogus token is 401", func(t *testing.T) {
		rec := doAuth(t, srv, stdhttp.MethodGet, "/queues/PICK/depth", "nope", "")
		assertProblem(t, rec, stdhttp.StatusUnauthorized, "unauthenticated")
	})
	t.Run("GET with read key is 2xx", func(t *testing.T) {
		if rec := doAuth(t, srv, stdhttp.MethodGet, "/queues/PICK/depth", testReadKey, ""); rec.Code != stdhttp.StatusOK {
			t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("POST with read key is 403 insufficient-scope", func(t *testing.T) {
		rec := doAuth(t, srv, stdhttp.MethodPost, "/stations", testReadKey, stationBody)
		assertProblem(t, rec, stdhttp.StatusForbidden, "insufficient-scope")
	})
	t.Run("POST without token is 401", func(t *testing.T) {
		rec := doAuth(t, srv, stdhttp.MethodPost, "/stations", "", stationBody)
		assertProblem(t, rec, stdhttp.StatusUnauthorized, "unauthenticated")
	})
	t.Run("POST with read-write key is 2xx", func(t *testing.T) {
		if rec := doAuth(t, srv, stdhttp.MethodPost, "/stations", testRWKey, stationBody); rec.Code != stdhttp.StatusCreated {
			t.Fatalf("want 201, got %d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("GET with read-write key is 2xx", func(t *testing.T) {
		if rec := doAuth(t, srv, stdhttp.MethodGet, "/queues/PICK/depth", testRWKey, ""); rec.Code != stdhttp.StatusOK {
			t.Fatalf("want 200, got %d", rec.Code)
		}
	})
}

// TestRouter_Auth_LogMode lets everything through (the rollout gate).
func TestRouter_Auth_LogMode(t *testing.T) {
	h, _, _, _, _ := newTestHandlers()
	srv := http.NewRouter(h, nil, http.WithAuth(testAuthn(), auth.ModeLog))
	if rec := doAuth(t, srv, stdhttp.MethodGet, "/queues/PICK/depth", "", ""); rec.Code != stdhttp.StatusOK {
		t.Fatalf("log mode must pass unauthenticated GET, got %d", rec.Code)
	}
	if rec := doAuth(t, srv, stdhttp.MethodPost, "/stations", testReadKey, `{"stationId":"st-log","capabilities":["pick"]}`); rec.Code != stdhttp.StatusCreated {
		t.Fatalf("log mode must pass under-scoped POST, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRouter_Auth_OffMode is the no-keys default: identical to no option.
func TestRouter_Auth_OffMode(t *testing.T) {
	h, _, _, _, _ := newTestHandlers()
	srv := http.NewRouter(h, nil, http.WithAuth(testAuthn(), auth.ModeOff))
	if rec := doAuth(t, srv, stdhttp.MethodPost, "/stations", "", `{"stationId":"st-off","capabilities":["pick"]}`); rec.Code != stdhttp.StatusCreated {
		t.Fatalf("off mode must be a no-op, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestReportsRouter_Auth_Enforce: every /reports route requires read;
// /healthz stays open.
func TestReportsRouter_Auth_Enforce(t *testing.T) {
	store := &fakeReportStore{report: report.ThroughputReport{}}
	srv := http.NewReportsRouter(&http.ReportsHandlers{Store: store}, nil, http.WithAuth(testAuthn(), auth.ModeEnforce))

	t.Run("healthz without token is 200", func(t *testing.T) {
		if rec := doAuth(t, srv, stdhttp.MethodGet, "/healthz", "", ""); rec.Code != stdhttp.StatusOK {
			t.Fatalf("want 200, got %d", rec.Code)
		}
	})
	t.Run("freshness without token is 401", func(t *testing.T) {
		rec := doAuth(t, srv, stdhttp.MethodGet, "/reports/throughput/freshness", "", "")
		assertProblem(t, rec, stdhttp.StatusUnauthorized, "unauthenticated")
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Fatal("401 must carry WWW-Authenticate")
		}
	})
	t.Run("freshness with read key is 200", func(t *testing.T) {
		if rec := doAuth(t, srv, stdhttp.MethodGet, "/reports/throughput/freshness", testReadKey, ""); rec.Code != stdhttp.StatusOK {
			t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("throughput with read-write key is 200", func(t *testing.T) {
		if rec := doAuth(t, srv, stdhttp.MethodGet, "/reports/throughput?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", testRWKey, ""); rec.Code != stdhttp.StatusOK {
			t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
		}
	})
}
