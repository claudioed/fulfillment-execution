package kafka_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/adapters/inbound/kafka"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/events"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

var epoch = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func idSeq(prefix string) func() shared.TaskId {
	n := 0
	return func() shared.TaskId {
		n++
		return shared.TaskId(prefix + string(rune('0'+n)))
	}
}

func newConsumer(t *testing.T) (*kafka.Consumer, *memory.TaskRepo) {
	t.Helper()
	tasks := memory.NewTaskRepo()
	createTask := &usecases.CreateTask{
		Tasks:     tasks,
		Publisher: events.NewBufferedPublisher(),
		Clock:     memory.NewFixedClock(epoch),
		NewId:     idSeq("t"),
	}
	c := &kafka.Consumer{
		CreateTask: createTask,
		Processed:  memory.NewProcessedEventsRepo(),
	}
	return c, tasks
}

func totalPending(t *testing.T, tasks *memory.TaskRepo) int {
	t.Helper()
	total := 0
	for _, tt := range []task.Type{task.Pick, task.Pack, task.Slam} {
		n, err := tasks.CountByTypeAndStatus(context.Background(), tt, task.Pending)
		if err != nil {
			t.Fatalf("CountByTypeAndStatus: %v", err)
		}
		total += n
	}
	return total
}

func workReleasedJSON(eventId, pathId, workUnitId string) []byte {
	return []byte(`{
		"event_id": "` + eventId + `",
		"event_type": "WorkReleased",
		"occurred_at": "2026-08-21T22:00:00Z",
		"source": "wes-work-planning",
		"data": {
			"path_id": "` + pathId + `",
			"work_unit_id": "` + workUnitId + `",
			"cpt": "2026-08-21T23:00:00Z",
			"ref": "release-1"
		}
	}`)
}

func workReleasedJSONWithFragile(eventId, pathId, workUnitId string, fragile bool) []byte {
	return []byte(`{
		"event_id": "` + eventId + `",
		"event_type": "WorkReleased",
		"occurred_at": "2026-08-21T22:00:00Z",
		"source": "wes-work-planning",
		"data": {
			"path_id": "` + pathId + `",
			"work_unit_id": "` + workUnitId + `",
			"cpt": "2026-08-21T23:00:00Z",
			"ref": "release-1",
			"fragile": ` + strconv.FormatBool(fragile) + `
		}
	}`)
}

func workReleasedJSONWithGiftWrap(eventId, pathId, workUnitId string, giftWrap bool) []byte {
	return []byte(`{
		"event_id": "` + eventId + `",
		"event_type": "WorkReleased",
		"occurred_at": "2026-08-21T22:00:00Z",
		"source": "wes-work-planning",
		"data": {
			"path_id": "` + pathId + `",
			"work_unit_id": "` + workUnitId + `",
			"cpt": "2026-08-21T23:00:00Z",
			"ref": "release-1",
			"gift_wrap": ` + strconv.FormatBool(giftWrap) + `
		}
	}`)
}

func TestHandleMessage_CreatesTaskFromWorkReleased(t *testing.T) {
	c, tasks := newConsumer(t)

	err := c.HandleMessage(context.Background(), workReleasedJSON("evt-1", "pick-abc", "wu-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := totalPending(t, tasks); got != 1 {
		t.Fatalf("expected exactly 1 task, got %d", got)
	}
	n, _ := tasks.CountByTypeAndStatus(context.Background(), task.Pick, task.Pending)
	if n != 1 {
		t.Fatalf("expected the task to be a Pick task, got %d Pick tasks", n)
	}
}

func TestHandleMessage_IgnoresNonWorkReleasedEvents(t *testing.T) {
	c, tasks := newConsumer(t)

	other := []byte(`{"event_id":"evt-2","event_type":"SomethingElse","data":{}}`)
	if err := c.HandleMessage(context.Background(), other); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := totalPending(t, tasks); got != 0 {
		t.Fatalf("expected no tasks created, got %d", got)
	}
}

func TestHandleMessage_DoubleDeliveryCreatesExactlyOneTask(t *testing.T) {
	c, tasks := newConsumer(t)

	msg := workReleasedJSON("evt-dup", "pack-xyz", "wu-2")
	if err := c.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("first delivery: unexpected error: %v", err)
	}
	if err := c.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("second delivery: unexpected error: %v", err)
	}

	if got := totalPending(t, tasks); got != 1 {
		t.Fatalf("expected exactly 1 task after double delivery, got %d", got)
	}
}

func TestHandleMessage_DerivesTaskTypeFromPathIdPrefix(t *testing.T) {
	c, tasks := newConsumer(t)

	cases := []struct {
		eventId  string
		pathId   string
		wantType task.Type
	}{
		{"evt-pick", "pick-1", task.Pick},
		{"evt-pack", "pack-1", task.Pack},
		{"evt-slam", "slam-1", task.Slam},
		{"evt-unknown", "unknown-1", task.Pick},
	}
	for _, tc := range cases {
		if err := c.HandleMessage(context.Background(), workReleasedJSON(tc.eventId, tc.pathId, "wu-derive")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	pick, _ := tasks.CountByTypeAndStatus(context.Background(), task.Pick, task.Pending)
	pack, _ := tasks.CountByTypeAndStatus(context.Background(), task.Pack, task.Pending)
	slam, _ := tasks.CountByTypeAndStatus(context.Background(), task.Slam, task.Pending)

	if pick != 2 { // "pick-1" plus the default for "unknown-1"
		t.Fatalf("expected 2 Pick tasks (explicit + default), got %d", pick)
	}
	if pack != 1 {
		t.Fatalf("expected 1 Pack task, got %d", pack)
	}
	if slam != 1 {
		t.Fatalf("expected 1 SLAM task, got %d", slam)
	}
}

// data.fragile is optional on WorkReleased for backward compatibility with
// producers that predate the field — a known simplification documented in
// this repo's README/INTEGRATION notes, matching the existing path_id
// prefix convention. Present-and-true must thread through to the created
// Task's Fragile flag.
func TestHandleMessage_ReadsOptionalFragileFlag(t *testing.T) {
	c, tasks := newConsumer(t)

	err := c.HandleMessage(context.Background(), workReleasedJSONWithFragile("evt-fragile", "pack-abc", "wu-fragile", true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	candidates, err := tasks.FindClaimableByType(context.Background(), task.Pack, epoch)
	if err != nil {
		t.Fatalf("FindClaimableByType: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected exactly 1 Pack task, got %d", len(candidates))
	}
	if !candidates[0].Fragile() {
		t.Fatalf("expected the created task to carry Fragile() == true")
	}
}

// Absent data.fragile (the shape produced by any already-documented
// producer that predates this field) must default to false rather than
// error or panic.
func TestHandleMessage_MissingFragileFieldDefaultsFalse(t *testing.T) {
	c, tasks := newConsumer(t)

	err := c.HandleMessage(context.Background(), workReleasedJSON("evt-no-fragile", "pick-abc", "wu-no-fragile"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	candidates, err := tasks.FindClaimableByType(context.Background(), task.Pick, epoch)
	if err != nil {
		t.Fatalf("FindClaimableByType: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected exactly 1 Pick task, got %d", len(candidates))
	}
	if candidates[0].Fragile() {
		t.Fatalf("expected Fragile() == false when data.fragile is absent from the envelope")
	}
}

// data.gift_wrap is optional on WorkReleased, omitted entirely (never
// published as explicit false) when gift wrap was not requested — see
// ADR-0011. Present-and-true must thread through to the created Task's
// GiftWrap flag.
func TestHandleMessage_ReadsOptionalGiftWrapFlag(t *testing.T) {
	c, tasks := newConsumer(t)

	err := c.HandleMessage(context.Background(), workReleasedJSONWithGiftWrap("evt-giftwrap", "pack-def", "wu-giftwrap", true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	candidates, err := tasks.FindClaimableByType(context.Background(), task.Pack, epoch)
	if err != nil {
		t.Fatalf("FindClaimableByType: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected exactly 1 Pack task, got %d", len(candidates))
	}
	if !candidates[0].GiftWrap() {
		t.Fatalf("expected the created task to carry GiftWrap() == true")
	}
}

// Absent data.gift_wrap (the common case: any WorkReleased that does not
// carry a gift-wrap request for the order) must default to false rather
// than error or panic.
func TestHandleMessage_MissingGiftWrapFieldDefaultsFalse(t *testing.T) {
	c, tasks := newConsumer(t)

	err := c.HandleMessage(context.Background(), workReleasedJSON("evt-no-giftwrap", "pick-def", "wu-no-giftwrap"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	candidates, err := tasks.FindClaimableByType(context.Background(), task.Pick, epoch)
	if err != nil {
		t.Fatalf("FindClaimableByType: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected exactly 1 Pick task, got %d", len(candidates))
	}
	if candidates[0].GiftWrap() {
		t.Fatalf("expected GiftWrap() == false when data.gift_wrap is absent from the envelope")
	}
}
