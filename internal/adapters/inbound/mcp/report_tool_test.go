package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	inboundmcp "github.com/claudioed/fulfillment-execution/internal/adapters/inbound/mcp"
)

// fakeReportsClient is a test double for the reports REST client the MCP tool
// delegates to.
type fakeReportsClient struct {
	report    inboundmcp.ThroughputReportView
	freshness inboundmcp.FreshnessView
	err       error
	lastQuery inboundmcp.ThroughputQuery
}

func (f *fakeReportsClient) GetThroughput(_ context.Context, q inboundmcp.ThroughputQuery) (inboundmcp.ThroughputReportView, error) {
	f.lastQuery = q
	return f.report, f.err
}

func (f *fakeReportsClient) GetFreshness(_ context.Context) (inboundmcp.FreshnessView, error) {
	return f.freshness, f.err
}

func TestReportTool_ForwardsFiltersAndReturnsRows(t *testing.T) {
	client := &fakeReportsClient{
		report: inboundmcp.ThroughputReportView{
			Rows: []inboundmcp.ThroughputRowView{
				{TaskType: "PICK", StationId: "st1", HourBucket: "2026-06-01T10:00:00Z", Completions: 3, AvgClaimToCompleteSeconds: 20, LeaseExpiries: 1},
			},
		},
	}

	out, err := inboundmcp.GetThroughputReportForTest(context.Background(), client, inboundmcp.ThroughputToolInput{
		From:        "2026-06-01T00:00:00Z",
		To:          "2026-06-02T00:00:00Z",
		TaskType:    "PICK",
		StationId:   "st1",
		Granularity: "hour",
	})
	if err != nil {
		t.Fatalf("tool: %v", err)
	}

	if client.lastQuery.From != "2026-06-01T00:00:00Z" || client.lastQuery.TaskType != "PICK" || client.lastQuery.StationId != "st1" {
		t.Errorf("filters not forwarded: %+v", client.lastQuery)
	}
	if len(out.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(out.Rows))
	}
	if out.Rows[0].Completions != 3 || out.Rows[0].TaskType != "PICK" {
		t.Errorf("row = %+v", out.Rows[0])
	}
}

func TestReportTool_RequiresFromTo(t *testing.T) {
	client := &fakeReportsClient{}
	tests := []inboundmcp.ThroughputToolInput{
		{To: "2026-06-02T00:00:00Z"},
		{From: "2026-06-01T00:00:00Z"},
	}
	for _, in := range tests {
		if _, err := inboundmcp.GetThroughputReportForTest(context.Background(), client, in); err == nil {
			t.Errorf("expected error for missing from/to, input=%+v", in)
		}
	}
}

// TestReportsRESTClient_CallsEndpoints verifies the real HTTP client hits the
// expected reports paths and decodes the responses.
func TestReportsRESTClient_CallsEndpoints(t *testing.T) {
	var gotPath, gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/reports/throughput":
			_ = json.NewEncoder(w).Encode(inboundmcp.ThroughputReportView{
				Rows: []inboundmcp.ThroughputRowView{{TaskType: "PICK", Completions: 7}},
			})
		case "/reports/throughput/freshness":
			_ = json.NewEncoder(w).Encode(inboundmcp.FreshnessView{LagSeconds: 12})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	c := inboundmcp.NewReportsRESTClient(ts.URL, ts.Client())

	rep, err := c.GetThroughput(context.Background(), inboundmcp.ThroughputQuery{
		From: "2026-06-01T00:00:00Z", To: "2026-06-02T00:00:00Z", TaskType: "PICK", Granularity: "hour",
	})
	if err != nil {
		t.Fatalf("GetThroughput: %v", err)
	}
	if gotPath != "/reports/throughput" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery == "" {
		t.Error("expected query string with filters")
	}
	if len(rep.Rows) != 1 || rep.Rows[0].Completions != 7 {
		t.Errorf("report = %+v", rep)
	}

	fr, err := c.GetFreshness(context.Background())
	if err != nil {
		t.Fatalf("GetFreshness: %v", err)
	}
	if fr.LagSeconds != 12 {
		t.Errorf("lag = %v, want 12", fr.LagSeconds)
	}
}

func TestReportsRESTClient_Non2xxIsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := inboundmcp.NewReportsRESTClient(ts.URL, ts.Client())
	if _, err := c.GetThroughput(context.Background(), inboundmcp.ThroughputQuery{From: "a", To: "b"}); err == nil {
		t.Error("expected error on 500 response")
	}
}
