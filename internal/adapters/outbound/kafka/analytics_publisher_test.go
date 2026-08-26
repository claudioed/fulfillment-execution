package kafka_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	outboundkafka "github.com/claudioed/fulfillment-execution/internal/adapters/outbound/kafka"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// fakeAnalyticsWriter captures the messages handed to WriteMessages so a test
// can assert on the published envelope without a live broker.
type fakeAnalyticsWriter struct {
	msgs []kafkago.Message
}

func (w *fakeAnalyticsWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func TestAnalyticsPublisher_PublishesEachEventType(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		name          string
		event         shared.DomainEvent
		wantType      string
		wantKey       string
		wantDataField string
		wantDataValue any
	}{
		{
			name:          "TaskCreated",
			event:         shared.NewTaskCreated("t1", at),
			wantType:      "TaskCreated",
			wantKey:       "t1",
			wantDataField: "task_id",
			wantDataValue: "t1",
		},
		{
			name:          "TaskClaimed",
			event:         shared.NewTaskClaimed("t2", "s2", at),
			wantType:      "TaskClaimed",
			wantKey:       "t2",
			wantDataField: "station_id",
			wantDataValue: "s2",
		},
		{
			name:          "LeaseExpired",
			event:         shared.NewLeaseExpired("t3", at),
			wantType:      "LeaseExpired",
			wantKey:       "t3",
			wantDataField: "task_id",
			wantDataValue: "t3",
		},
		{
			name:          "TaskCompleted",
			event:         shared.NewTaskCompleted("t4", "s4", at),
			wantType:      "TaskCompleted",
			wantKey:       "t4",
			wantDataField: "station_id",
			wantDataValue: "s4",
		},
		{
			name:          "ItemPicked",
			event:         shared.NewItemPicked("t5", at),
			wantType:      "ItemPicked",
			wantKey:       "t5",
			wantDataField: "task_id",
			wantDataValue: "t5",
		},
		{
			name:          "PackageSealed",
			event:         shared.NewPackageSealed("p6", at),
			wantType:      "PackageSealed",
			wantKey:       "p6",
			wantDataField: "package_id",
			wantDataValue: "p6",
		},
		{
			name:          "WeightDiscrepancyDetected",
			event:         shared.NewWeightDiscrepancyDetected("p7", 1000, 1200, at),
			wantType:      "WeightDiscrepancyDetected",
			wantKey:       "p7",
			wantDataField: "actual_g",
			wantDataValue: float64(1200),
		},
		{
			name:          "LabelApplied",
			event:         shared.NewLabelApplied("p8", at),
			wantType:      "LabelApplied",
			wantKey:       "p8",
			wantDataField: "package_id",
			wantDataValue: "p8",
		},
		{
			name:          "PackageDiverted",
			event:         shared.NewPackageDiverted("p9", at),
			wantType:      "PackageDiverted",
			wantKey:       "p9",
			wantDataField: "package_id",
			wantDataValue: "p9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &fakeAnalyticsWriter{}
			p := outboundkafka.NewAnalyticsPublisher(nil, func() string { return "evt-fixed" })
			p.Writer = w

			if err := p.Publish(context.Background(), tt.event); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if len(w.msgs) != 1 {
				t.Fatalf("expected 1 message, got %d", len(w.msgs))
			}
			msg := w.msgs[0]
			if string(msg.Key) != tt.wantKey {
				t.Errorf("key = %q, want %q", string(msg.Key), tt.wantKey)
			}

			var env outboundkafka.AnalyticsEnvelope
			if err := json.Unmarshal(msg.Value, &env); err != nil {
				t.Fatalf("unmarshal envelope: %v", err)
			}
			if env.EventType != tt.wantType {
				t.Errorf("event_type = %q, want %q", env.EventType, tt.wantType)
			}
			if env.EventId != "evt-fixed" {
				t.Errorf("event_id = %q, want evt-fixed", env.EventId)
			}
			if env.Source != "fulfillment-execution" {
				t.Errorf("source = %q, want fulfillment-execution", env.Source)
			}
			if env.SchemaVersion != 1 {
				t.Errorf("schema_version = %d, want 1", env.SchemaVersion)
			}
			if !env.OccurredAt.Equal(at) {
				t.Errorf("occurred_at = %v, want %v", env.OccurredAt, at)
			}

			var data map[string]any
			if err := json.Unmarshal(env.Data, &data); err != nil {
				t.Fatalf("unmarshal data: %v", err)
			}
			if got := data[tt.wantDataField]; got != tt.wantDataValue {
				t.Errorf("data[%q] = %v (%T), want %v (%T)", tt.wantDataField, got, got, tt.wantDataValue, tt.wantDataValue)
			}
		})
	}
}

func TestAnalyticsPublisher_SkipsUnknownEvents(t *testing.T) {
	w := &fakeAnalyticsWriter{}
	p := outboundkafka.NewAnalyticsPublisher(nil, func() string { return "evt" })
	p.Writer = w

	if err := p.Publish(context.Background(), unknownEvent{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(w.msgs) != 0 {
		t.Fatalf("expected unknown event to be skipped, got %d messages", len(w.msgs))
	}
}

type unknownEvent struct{}

func (unknownEvent) EventName() string     { return "Unknown" }
func (unknownEvent) OccurredAt() time.Time { return time.Time{} }

func TestAnalyticsPublisher_WeightDiscrepancyExpectedActual(t *testing.T) {
	w := &fakeAnalyticsWriter{}
	p := outboundkafka.NewAnalyticsPublisher(nil, func() string { return "evt" })
	p.Writer = w

	at := time.Now()
	if err := p.Publish(context.Background(), shared.NewWeightDiscrepancyDetected("pkg", 900, 1100, at)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	var env outboundkafka.AnalyticsEnvelope
	if err := json.Unmarshal(w.msgs[0].Value, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if data["expected_g"] != float64(900) {
		t.Errorf("expected_g = %v, want 900", data["expected_g"])
	}
	if data["package_id"] != "pkg" {
		t.Errorf("package_id = %v, want pkg", data["package_id"])
	}
}
