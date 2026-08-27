package http_test

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/adapters/inbound/http"
	"github.com/claudioed/fulfillment-execution/internal/analytics/report"
)

// fakeReportStore is a test double for report.ReportStore.
type fakeReportStore struct {
	report    report.ThroughputReport
	lag       time.Duration
	queryErr  error
	freshErr  error
	lastQuery report.ReportQuery
}

func (f *fakeReportStore) Query(_ context.Context, q report.ReportQuery) (report.ThroughputReport, error) {
	f.lastQuery = q
	return f.report, f.queryErr
}

func (f *fakeReportStore) FreshnessLag(_ context.Context) (time.Duration, error) {
	return f.lag, f.freshErr
}

func newReportsServer(store report.ReportStore) stdhttp.Handler {
	return http.NewReportsRouter(&http.ReportsHandlers{Store: store}, nil)
}

func TestReportsThroughput_OK(t *testing.T) {
	bucket := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	store := &fakeReportStore{
		report: report.ThroughputReport{Rows: []report.Row{
			{
				Key:                       report.RowKey{TaskType: "PICK", StationId: "st1", HourBucket: bucket},
				Completions:               5,
				AvgClaimToCompleteSeconds: 42.5,
				LeaseExpiries:             1,
				WeighCheckDiverts:         0,
			},
		}},
	}
	srv := newReportsServer(store)

	req := httptest.NewRequest(stdhttp.MethodGet,
		"/reports/throughput?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z&taskType=PICK&stationId=st1&granularity=hour", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Filters must be forwarded to the store.
	if store.lastQuery.TaskType != "PICK" || store.lastQuery.StationId != "st1" {
		t.Errorf("filters not forwarded: %+v", store.lastQuery)
	}
	if store.lastQuery.Granularity != report.GranularityHour {
		t.Errorf("granularity = %q, want hour", store.lastQuery.Granularity)
	}

	var body struct {
		Rows []struct {
			TaskType                  string  `json:"taskType"`
			StationId                 string  `json:"stationId"`
			HourBucket                string  `json:"hourBucket"`
			Completions               int     `json:"completions"`
			AvgClaimToCompleteSeconds float64 `json:"avgClaimToCompleteSeconds"`
			LeaseExpiries             int     `json:"leaseExpiries"`
			WeighCheckDiverts         int     `json:"weighCheckDiverts"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(body.Rows))
	}
	r := body.Rows[0]
	if r.TaskType != "PICK" || r.StationId != "st1" || r.Completions != 5 || r.AvgClaimToCompleteSeconds != 42.5 || r.LeaseExpiries != 1 {
		t.Errorf("row = %+v", r)
	}
	if r.HourBucket != "2026-06-01T10:00:00Z" {
		t.Errorf("hourBucket = %q", r.HourBucket)
	}
}

func TestReportsThroughput_MissingFromTo(t *testing.T) {
	srv := newReportsServer(&fakeReportStore{})
	tests := []struct {
		name string
		url  string
	}{
		{"no from", "/reports/throughput?to=2026-06-02T00:00:00Z"},
		{"no to", "/reports/throughput?from=2026-06-01T00:00:00Z"},
		{"bad from", "/reports/throughput?from=nope&to=2026-06-02T00:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, tt.url, nil))
			if rec.Code != stdhttp.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
				t.Errorf("content-type = %q, want application/problem+json", ct)
			}
		})
	}
}

func TestReportsThroughput_DefaultGranularity(t *testing.T) {
	store := &fakeReportStore{}
	srv := newReportsServer(store)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet,
		"/reports/throughput?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", nil))
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if store.lastQuery.Granularity != report.GranularityHour {
		t.Errorf("default granularity = %q, want hour", store.lastQuery.Granularity)
	}
}

func TestReportsFreshness_OK(t *testing.T) {
	store := &fakeReportStore{lag: 90 * time.Second}
	srv := newReportsServer(store)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/reports/throughput/freshness", nil))
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		LagSeconds float64 `json:"lagSeconds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.LagSeconds != 90 {
		t.Errorf("lagSeconds = %v, want 90", body.LagSeconds)
	}
}

func TestReportsHealthz(t *testing.T) {
	srv := newReportsServer(&fakeReportStore{})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/healthz", nil))
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
