package mcp_test

import (
	"context"
	"testing"

	inboundmcp "github.com/claudioed/fulfillment-execution/internal/adapters/inbound/mcp"
)

// TestOnTimeToCPTTool_AggregatesAcrossBucketsAndDerivesRate asserts the
// tool sums PackagesManifested/PackagesOnTimeToCPT/PackagesLateToCPT across
// every hour bucket the underlying throughput query returns and derives
// OnTimeRate from the summed counts (never stored, matching the report's
// raw-counts convention — ADR-0026).
func TestOnTimeToCPTTool_AggregatesAcrossBucketsAndDerivesRate(t *testing.T) {
	client := &fakeReportsClient{
		report: inboundmcp.ThroughputReportView{
			Rows: []inboundmcp.ThroughputRowView{
				{TaskType: "SLAM", StationId: "st1", HourBucket: "2026-06-01T10:00:00Z", PackagesManifested: 5, PackagesOnTimeToCPT: 4, PackagesLateToCPT: 1},
				{TaskType: "SLAM", StationId: "st1", HourBucket: "2026-06-01T11:00:00Z", PackagesManifested: 3, PackagesOnTimeToCPT: 3, PackagesLateToCPT: 0},
			},
		},
	}

	out, err := inboundmcp.GetOnTimeToCPTForTest(context.Background(), client, inboundmcp.OnTimeToCPTToolInput{
		From: "2026-06-01T00:00:00Z", To: "2026-06-02T00:00:00Z", TaskType: "SLAM", StationId: "st1",
	})
	if err != nil {
		t.Fatalf("tool: %v", err)
	}

	if out.PackagesManifested != 8 {
		t.Errorf("PackagesManifested = %d, want 8", out.PackagesManifested)
	}
	if out.PackagesOnTimeToCPT != 7 {
		t.Errorf("PackagesOnTimeToCPT = %d, want 7", out.PackagesOnTimeToCPT)
	}
	if out.PackagesLateToCPT != 1 {
		t.Errorf("PackagesLateToCPT = %d, want 1", out.PackagesLateToCPT)
	}
	wantRate := 7.0 / 8.0
	if out.OnTimeRate != wantRate {
		t.Errorf("OnTimeRate = %v, want %v", out.OnTimeRate, wantRate)
	}
	if client.lastQuery.TaskType != "SLAM" || client.lastQuery.StationId != "st1" {
		t.Errorf("filters not forwarded: %+v", client.lastQuery)
	}
}

// TestOnTimeToCPTTool_ZeroManifestedYieldsZeroRateNotDivideByZero asserts
// an empty window (no manifested packages) reports rate 0 rather than
// panicking or returning NaN.
func TestOnTimeToCPTTool_ZeroManifestedYieldsZeroRateNotDivideByZero(t *testing.T) {
	client := &fakeReportsClient{report: inboundmcp.ThroughputReportView{}}

	out, err := inboundmcp.GetOnTimeToCPTForTest(context.Background(), client, inboundmcp.OnTimeToCPTToolInput{
		From: "2026-06-01T00:00:00Z", To: "2026-06-02T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("tool: %v", err)
	}
	if out.PackagesManifested != 0 {
		t.Errorf("PackagesManifested = %d, want 0", out.PackagesManifested)
	}
	if out.OnTimeRate != 0 {
		t.Errorf("OnTimeRate = %v, want 0", out.OnTimeRate)
	}
}

func TestOnTimeToCPTTool_RequiresFromTo(t *testing.T) {
	client := &fakeReportsClient{}
	tests := []inboundmcp.OnTimeToCPTToolInput{
		{To: "2026-06-02T00:00:00Z"},
		{From: "2026-06-01T00:00:00Z"},
	}
	for _, in := range tests {
		if _, err := inboundmcp.GetOnTimeToCPTForTest(context.Background(), client, in); err == nil {
			t.Errorf("expected error for missing from/to, input=%+v", in)
		}
	}
}

func TestOnTimeToCPTTool_NilClientErrors(t *testing.T) {
	if _, err := inboundmcp.GetOnTimeToCPTForTest(context.Background(), nil, inboundmcp.OnTimeToCPTToolInput{
		From: "2026-06-01T00:00:00Z", To: "2026-06-02T00:00:00Z",
	}); err == nil {
		t.Error("expected error for nil reports client")
	}
}
