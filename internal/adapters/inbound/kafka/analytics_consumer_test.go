package kafka_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	inboundkafka "github.com/claudioed/fulfillment-execution/internal/adapters/inbound/kafka"
)

// call captures one projection-store method invocation.
type call struct {
	method    string
	eventId   string
	taskId    string
	taskType  string
	stationId string
	at        time.Time
}

// fakeProjection records the calls the consumer makes so a test can assert
// the envelope was routed to the right method with the right fields.
type fakeProjection struct {
	calls []call
}

func (f *fakeProjection) ApplyTaskClaimed(_ context.Context, eventId, taskId, taskType, stationId string, at time.Time) error {
	f.calls = append(f.calls, call{"claimed", eventId, taskId, taskType, stationId, at})
	return nil
}
func (f *fakeProjection) ApplyTaskCompleted(_ context.Context, eventId, taskId, taskType, stationId string, at time.Time) error {
	f.calls = append(f.calls, call{"completed", eventId, taskId, taskType, stationId, at})
	return nil
}
func (f *fakeProjection) ApplyLeaseExpired(_ context.Context, eventId, taskId, taskType, stationId string, at time.Time) error {
	f.calls = append(f.calls, call{"lease", eventId, taskId, taskType, stationId, at})
	return nil
}
func (f *fakeProjection) ApplyWeightDiscrepancy(_ context.Context, eventId, taskType, stationId string, at time.Time) error {
	f.calls = append(f.calls, call{"divert", eventId, "", taskType, stationId, at})
	return nil
}

// fakeProcessed is an in-memory ports.ProcessedEvents.
type fakeProcessed struct {
	seen map[string]bool
}

func newFakeProcessed() *fakeProcessed { return &fakeProcessed{seen: map[string]bool{}} }

func (p *fakeProcessed) MarkProcessed(_ context.Context, eventId string) (bool, error) {
	if p.seen[eventId] {
		return false, nil
	}
	p.seen[eventId] = true
	return true, nil
}

func envelope(t *testing.T, eventId, eventType string, at time.Time, data map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	env := map[string]any{
		"event_id":       eventId,
		"event_type":     eventType,
		"occurred_at":    at.Format(time.RFC3339Nano),
		"source":         "fulfillment-execution",
		"schema_version": 1,
		"data":           json.RawMessage(raw),
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return b
}

func TestAnalyticsConsumer_RoutesEachEventType(t *testing.T) {
	at := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		eventType  string
		data       map[string]any
		wantMethod string
	}{
		{"claimed", "TaskClaimed", map[string]any{"task_id": "T1", "station_id": "st1"}, "claimed"},
		{"completed", "TaskCompleted", map[string]any{"task_id": "T1", "station_id": "st1"}, "completed"},
		{"lease", "LeaseExpired", map[string]any{"task_id": "T1"}, "lease"},
		{"divert", "WeightDiscrepancyDetected", map[string]any{"package_id": "P1"}, "divert"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proj := &fakeProjection{}
			processed := newFakeProcessed()
			c := &inboundkafka.AnalyticsConsumer{Projection: proj, Processed: processed, Logger: slog.Default()}

			raw := envelope(t, "e-"+tt.name, tt.eventType, at, tt.data)
			if err := c.HandleMessage(context.Background(), raw); err != nil {
				t.Fatalf("HandleMessage: %v", err)
			}
			if len(proj.calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(proj.calls))
			}
			if proj.calls[0].method != tt.wantMethod {
				t.Errorf("method = %q, want %q", proj.calls[0].method, tt.wantMethod)
			}
			if !proj.calls[0].at.Equal(at) {
				t.Errorf("at = %v, want %v", proj.calls[0].at, at)
			}
		})
	}
}

func TestAnalyticsConsumer_Idempotent(t *testing.T) {
	at := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	proj := &fakeProjection{}
	processed := newFakeProcessed()
	c := &inboundkafka.AnalyticsConsumer{Projection: proj, Processed: processed, Logger: slog.Default()}

	raw := envelope(t, "dup", "TaskCompleted", at, map[string]any{"task_id": "T1", "station_id": "st1"})
	for range 2 {
		if err := c.HandleMessage(context.Background(), raw); err != nil {
			t.Fatalf("HandleMessage: %v", err)
		}
	}
	if len(proj.calls) != 1 {
		t.Fatalf("expected 1 apply for duplicate delivery, got %d", len(proj.calls))
	}
}

func TestAnalyticsConsumer_IgnoresUnknownEventType(t *testing.T) {
	proj := &fakeProjection{}
	processed := newFakeProcessed()
	c := &inboundkafka.AnalyticsConsumer{Projection: proj, Processed: processed, Logger: slog.Default()}

	raw := envelope(t, "e1", "TaskCreated", time.Now(), map[string]any{"task_id": "T1"})
	if err := c.HandleMessage(context.Background(), raw); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if len(proj.calls) != 0 {
		t.Fatalf("expected unknown/non-projecting event to make no call, got %d", len(proj.calls))
	}
	// An event with no projection method must NOT be marked processed, so a
	// later contract change could reprocess it.
	if processed.seen["e1"] {
		t.Error("non-projecting event should not be marked processed")
	}
}
