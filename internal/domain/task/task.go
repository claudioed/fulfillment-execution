// Package task implements the Task aggregate: the unit of physical work
// (Pick, Pack, or SLAM) that moves through Pending -> Claimed(leased) ->
// Completed, or has its lease expire back to Pending.
package task

import (
	"errors"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// Type is the process path a task belongs to.
type Type string

const (
	Pick Type = "PICK"
	Pack Type = "PACK"
	Slam Type = "SLAM"
)

// Status is the lifecycle state of a task.
type Status string

const (
	Pending   Status = "PENDING"
	Claimed   Status = "CLAIMED"
	Completed Status = "COMPLETED"
)

var (
	// ErrCapabilityMismatch is returned when a station lacks the
	// capabilities required by the task it is trying to claim.
	ErrCapabilityMismatch = errors.New("task: station capabilities do not match required capabilities")
	// ErrAlreadyClaimed is returned when a claim is attempted on a task
	// that already has an active (unexpired) claim — enforces at-most-once.
	ErrAlreadyClaimed = errors.New("task: already claimed")
	// ErrNotClaimed is returned when an operation requiring an active claim
	// (renew, complete) is attempted on a task with no active claim.
	ErrNotClaimed = errors.New("task: not claimed")
	// ErrNotOwner is returned when a station attempts to renew or complete
	// a claim it does not own.
	ErrNotOwner = errors.New("task: station does not own the claim")
	// ErrAlreadyCompleted is returned on any attempt to act on a completed task.
	ErrAlreadyCompleted = errors.New("task: already completed")
)

// Lease represents a time-boxed claim on a task by a station. If not
// confirmed/completed before Expiry, the task returns to Pending.
type Lease struct {
	StationId shared.StationId
	Expiry    time.Time
}

func (l Lease) expired(now time.Time) bool {
	return !now.Before(l.Expiry)
}

// Task is the aggregate root for a unit of Pick, Pack, or SLAM work.
type Task struct {
	id                   shared.TaskId
	taskType             Type
	status               Status
	cpt                  shared.CPT
	orderRef             shared.OrderRef
	requiredCapabilities shared.CapabilitySet
	lease                *Lease
}

// New creates a task in the Pending state, ready for the pool.
func New(id shared.TaskId, taskType Type, cpt shared.CPT, orderRef shared.OrderRef, required shared.CapabilitySet) *Task {
	return &Task{
		id:                   id,
		taskType:             taskType,
		status:               Pending,
		cpt:                  cpt,
		orderRef:             orderRef,
		requiredCapabilities: required,
	}
}

// Rehydrate reconstructs a Task from persisted state without re-validating
// construction invariants (used by repository adapters).
func Rehydrate(id shared.TaskId, taskType Type, status Status, cpt shared.CPT, orderRef shared.OrderRef, required shared.CapabilitySet, lease *Lease) *Task {
	return &Task{
		id:                   id,
		taskType:             taskType,
		status:               status,
		cpt:                  cpt,
		orderRef:             orderRef,
		requiredCapabilities: required,
		lease:                lease,
	}
}

func (t *Task) Id() shared.TaskId                          { return t.id }
func (t *Task) Type() Type                                 { return t.taskType }
func (t *Task) Status() Status                             { return t.status }
func (t *Task) CPT() shared.CPT                            { return t.cpt }
func (t *Task) OrderRef() shared.OrderRef                  { return t.orderRef }
func (t *Task) RequiredCapabilities() shared.CapabilitySet { return t.requiredCapabilities }
func (t *Task) Lease() *Lease                              { return t.lease }

// IsAvailable reports whether the task can be claimed at `now`: it is
// Pending, or Claimed with an expired lease (which frees it in the caller's
// view without mutating state — callers should call ExpireLeaseIfDue first
// for state that must be persisted).
func (t *Task) IsAvailable(now time.Time) bool {
	if t.status == Pending {
		return true
	}
	if t.status == Claimed && t.lease != nil && t.lease.expired(now) {
		return true
	}
	return false
}

// ExpireLeaseIfDue frees a Claimed task whose lease has passed, returning it
// to Pending. Returns true if it freed the task.
func (t *Task) ExpireLeaseIfDue(now time.Time) bool {
	if t.status != Claimed || t.lease == nil || !t.lease.expired(now) {
		return false
	}
	t.status = Pending
	t.lease = nil
	return true
}

// Claim assigns the task to a station for the given lease duration, enforcing
// at-most-once assignment and capability match.
func (t *Task) Claim(stationId shared.StationId, stationCapabilities shared.CapabilitySet, now time.Time, leaseDuration time.Duration) error {
	if t.status == Completed {
		return ErrAlreadyCompleted
	}
	// An expired lease implicitly frees the task before re-evaluating the claim.
	t.ExpireLeaseIfDue(now)
	if t.status == Claimed {
		return ErrAlreadyClaimed
	}
	if !stationCapabilities.HasAll(t.requiredCapabilities) {
		return ErrCapabilityMismatch
	}
	t.status = Claimed
	t.lease = &Lease{StationId: stationId, Expiry: now.Add(leaseDuration)}
	return nil
}

// RenewLease extends an active claim's lease. Only the owning station may renew.
func (t *Task) RenewLease(stationId shared.StationId, now time.Time, leaseDuration time.Duration) error {
	if t.status == Completed {
		return ErrAlreadyCompleted
	}
	if t.status != Claimed || t.lease == nil {
		return ErrNotClaimed
	}
	if t.lease.expired(now) {
		t.status = Pending
		t.lease = nil
		return ErrNotClaimed
	}
	if t.lease.StationId != stationId {
		return ErrNotOwner
	}
	t.lease.Expiry = now.Add(leaseDuration)
	return nil
}

// Complete finishes the task. Only the owning station may complete it, the
// claim must still be active, and a completed task cannot be completed
// again (no double-complete).
func (t *Task) Complete(stationId shared.StationId, now time.Time) error {
	if t.status == Completed {
		return ErrAlreadyCompleted
	}
	if t.status != Claimed || t.lease == nil {
		return ErrNotClaimed
	}
	if t.lease.expired(now) {
		t.status = Pending
		t.lease = nil
		return ErrNotClaimed
	}
	if t.lease.StationId != stationId {
		return ErrNotOwner
	}
	t.status = Completed
	t.lease = nil
	return nil
}
