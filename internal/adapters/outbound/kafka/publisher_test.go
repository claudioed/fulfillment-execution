package kafka_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	outboundkafka "github.com/claudioed/fulfillment-execution/internal/adapters/outbound/kafka"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/station"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

var epoch = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// fakeWriter records every message it is asked to write, so tests can
// assert on the published envelope without a live broker.
type fakeWriter struct {
	mu   sync.Mutex
	msgs []kafkago.Message
}

func (w *fakeWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func newTestTask(t *testing.T, tasks *memory.TaskRepo, orderRef shared.OrderRef) *task.Task {
	t.Helper()
	tk := task.New("task-1", task.Pick, shared.NewCPT(epoch.Add(time.Hour)), orderRef, shared.NewCapabilitySet("pick"), false, false)
	if err := tasks.Save(context.Background(), tk); err != nil {
		t.Fatalf("save task: %v", err)
	}
	return tk
}

func TestPublish_PublishesTaskCompletedEnrichedWithOrderRef(t *testing.T) {
	tasks := memory.NewTaskRepo()
	newTestTask(t, tasks, shared.OrderRef("wu-original"))

	w := &fakeWriter{}
	ids := []string{"evt-1"}
	i := 0
	p := &outboundkafka.Publisher{
		Writer: w,
		Tasks:  tasks,
		NewId:  func() string { id := ids[i]; i++; return id },
	}

	evt := shared.NewTaskCompleted("task-1", "station-1", epoch)
	if err := p.Publish(context.Background(), evt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(w.msgs) != 1 {
		t.Fatalf("expected exactly 1 published message, got %d", len(w.msgs))
	}

	var env outboundkafka.Envelope
	if err := json.Unmarshal(w.msgs[0].Value, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	if env.EventId != "evt-1" {
		t.Errorf("EventId = %q, want %q", env.EventId, "evt-1")
	}
	if env.EventType != "TaskCompleted" {
		t.Errorf("EventType = %q, want %q", env.EventType, "TaskCompleted")
	}
	if env.Source != "fulfillment-execution" {
		t.Errorf("Source = %q, want %q", env.Source, "fulfillment-execution")
	}
	if !env.OccurredAt.Equal(epoch) {
		t.Errorf("OccurredAt = %v, want %v", env.OccurredAt, epoch)
	}
	if env.Data.TaskId != "task-1" {
		t.Errorf("Data.TaskId = %q, want %q", env.Data.TaskId, "task-1")
	}
	if env.Data.StationId != "station-1" {
		t.Errorf("Data.StationId = %q, want %q", env.Data.StationId, "station-1")
	}
	if env.Data.WorkUnitId != "wu-original" {
		t.Errorf("Data.WorkUnitId = %q, want %q — enrichment via TaskRepo lookup failed", env.Data.WorkUnitId, "wu-original")
	}
	if env.Data.TaskType != "PICK" {
		t.Errorf("Data.TaskType = %q, want %q — enrichment via TaskRepo lookup failed", env.Data.TaskType, "PICK")
	}
}

func TestPublish_IgnoresNonTaskCompletedEvents(t *testing.T) {
	tasks := memory.NewTaskRepo()
	w := &fakeWriter{}
	p := &outboundkafka.Publisher{
		Writer: w,
		Tasks:  tasks,
		NewId:  func() string { return "evt-unused" },
	}

	if err := p.Publish(context.Background(), shared.NewTaskCreated("task-1", epoch)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(w.msgs) != 0 {
		t.Fatalf("expected no published messages for TaskCreated, got %d", len(w.msgs))
	}
}

// TaskCompleted is enriched with the completing associate's identity
// (the occupant of the claiming station at publish time) and the task's
// duration (completion time minus the claim's start time) — see ADR-0014.
func TestPublish_EnrichesWithAssociateIdAndDurationSeconds(t *testing.T) {
	tasks := memory.NewTaskRepo()
	stations := memory.NewStationRepo()

	tk := task.New("task-1", task.Pick, shared.NewCPT(epoch.Add(time.Hour)), "order-1", shared.NewCapabilitySet("pick"), false, false)
	if err := tk.Claim("station-1", shared.NewCapabilitySet("pick"), epoch, 5*time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}
	completedAt := epoch.Add(90 * time.Second)
	if err := tk.Complete("station-1", completedAt); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := tasks.Save(context.Background(), tk); err != nil {
		t.Fatalf("save task: %v", err)
	}

	st := station.New("station-1", shared.NewCapabilitySet("pick"))
	if err := st.CheckIn("worker-42"); err != nil {
		t.Fatalf("check in: %v", err)
	}
	if err := stations.Save(context.Background(), st); err != nil {
		t.Fatalf("save station: %v", err)
	}

	w := &fakeWriter{}
	p := &outboundkafka.Publisher{
		Writer:   w,
		Tasks:    tasks,
		Stations: stations,
		NewId:    func() string { return "evt-1" },
	}

	evt := shared.NewTaskCompleted("task-1", "station-1", completedAt)
	if err := p.Publish(context.Background(), evt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env outboundkafka.Envelope
	if err := json.Unmarshal(w.msgs[0].Value, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.Data.AssociateId != "worker-42" {
		t.Errorf("Data.AssociateId = %q, want %q", env.Data.AssociateId, "worker-42")
	}
	if env.Data.DurationSeconds != 90 {
		t.Errorf("Data.DurationSeconds = %d, want 90", env.Data.DurationSeconds)
	}
}

// AssociateId is a soft/optional fact: a station with no checked-in
// occupant (e.g. a robot) enriches with an empty string, not an error.
func TestPublish_AssociateIdEmptyWhenStationHasNoOccupant(t *testing.T) {
	tasks := memory.NewTaskRepo()
	stations := memory.NewStationRepo()

	tk := task.New("task-1", task.Pick, shared.NewCPT(epoch.Add(time.Hour)), "order-1", shared.NewCapabilitySet("pick"), false, false)
	_ = tk.Claim("station-1", shared.NewCapabilitySet("pick"), epoch, 5*time.Minute)
	_ = tk.Complete("station-1", epoch.Add(time.Minute))
	_ = tasks.Save(context.Background(), tk)
	_ = stations.Save(context.Background(), station.New("station-1", shared.NewCapabilitySet("pick")))

	w := &fakeWriter{}
	p := &outboundkafka.Publisher{Writer: w, Tasks: tasks, Stations: stations, NewId: func() string { return "evt-1" }}

	evt := shared.NewTaskCompleted("task-1", "station-1", epoch.Add(time.Minute))
	if err := p.Publish(context.Background(), evt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env outboundkafka.Envelope
	if err := json.Unmarshal(w.msgs[0].Value, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.Data.AssociateId != "" {
		t.Errorf("Data.AssociateId = %q, want empty (no occupant)", env.Data.AssociateId)
	}
}

// DurationSeconds is 0 when the task's ClaimedAt is nil (e.g. a task
// persisted before this field existed) rather than a negative or garbage
// value.
func TestPublish_DurationSecondsZeroWhenClaimedAtNil(t *testing.T) {
	tasks := memory.NewTaskRepo()

	// Rehydrate simulates a pre-migration row: Claimed status but no
	// recorded claimedAt.
	lease := &task.Lease{StationId: "station-1", Expiry: epoch.Add(time.Hour)}
	tk := task.Rehydrate("task-1", task.Pick, task.Claimed, shared.NewCPT(epoch.Add(time.Hour)), "order-1", shared.NewCapabilitySet("pick"), lease, false, false, nil)
	_ = tk.Complete("station-1", epoch.Add(time.Minute))
	_ = tasks.Save(context.Background(), tk)

	w := &fakeWriter{}
	p := &outboundkafka.Publisher{Writer: w, Tasks: tasks, NewId: func() string { return "evt-1" }}

	evt := shared.NewTaskCompleted("task-1", "station-1", epoch.Add(time.Minute))
	if err := p.Publish(context.Background(), evt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env outboundkafka.Envelope
	if err := json.Unmarshal(w.msgs[0].Value, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.Data.DurationSeconds != 0 {
		t.Errorf("Data.DurationSeconds = %d, want 0 when ClaimedAt is nil", env.Data.DurationSeconds)
	}
}

// TaskCompleted is enriched with the completed task's own type (ADR-0023),
// read directly off the already-loaded Task returned by the same TaskRepo
// lookup WorkUnitId already uses — no new repo dependency. Exercised
// across all four task types this service models, not just PICK, since a
// hardcoded single-type fixture elsewhere in this file could otherwise
// mask a mapping bug for PACK/SLAM/REBIN.
func TestPublish_EnrichesWithTaskType(t *testing.T) {
	tests := []struct {
		name     string
		taskType task.Type
	}{
		{name: "pick", taskType: task.Pick},
		{name: "pack", taskType: task.Pack},
		{name: "slam", taskType: task.Slam},
		{name: "rebin", taskType: task.Rebin},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tasks := memory.NewTaskRepo()
			tk := task.New("task-1", tt.taskType, shared.NewCPT(epoch.Add(time.Hour)), "order-1", shared.NewCapabilitySet("pick"), false, false)
			if err := tasks.Save(context.Background(), tk); err != nil {
				t.Fatalf("save task: %v", err)
			}

			w := &fakeWriter{}
			p := &outboundkafka.Publisher{Writer: w, Tasks: tasks, NewId: func() string { return "evt-1" }}

			evt := shared.NewTaskCompleted("task-1", "station-1", epoch)
			if err := p.Publish(context.Background(), evt); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var env outboundkafka.Envelope
			if err := json.Unmarshal(w.msgs[0].Value, &env); err != nil {
				t.Fatalf("unmarshal envelope: %v", err)
			}
			if env.Data.TaskType != string(tt.taskType) {
				t.Errorf("Data.TaskType = %q, want %q", env.Data.TaskType, string(tt.taskType))
			}
		})
	}
}

// TaskType is "" (omitted on the wire, like AssociateId/DurationSeconds)
// when the completed Task cannot be found — the enrichment degrades the
// same way every other repo-lookup-derived field on this envelope already
// does, rather than panicking on a nil Task.
func TestPublish_TaskTypeEmptyWhenTaskNotFound(t *testing.T) {
	tasks := memory.NewTaskRepo()
	w := &fakeWriter{}
	p := &outboundkafka.Publisher{Writer: w, Tasks: tasks, NewId: func() string { return "evt-1" }}

	evt := shared.NewTaskCompleted("task-missing", "station-1", epoch)
	if err := p.Publish(context.Background(), evt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env outboundkafka.Envelope
	if err := json.Unmarshal(w.msgs[0].Value, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.Data.TaskType != "" {
		t.Errorf("Data.TaskType = %q, want empty when the task cannot be found", env.Data.TaskType)
	}
}
