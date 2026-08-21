package shared

import "time"

// DomainEvent is a past-tense fact raised by an aggregate.
type DomainEvent interface {
	EventName() string
	OccurredAt() time.Time
}

type base struct {
	Name string
	At   time.Time
}

func (b base) EventName() string     { return b.Name }
func (b base) OccurredAt() time.Time { return b.At }

// TaskCreated is raised when a new task enters the pool.
type TaskCreated struct {
	base
	TaskId TaskId
}

func NewTaskCreated(id TaskId, at time.Time) TaskCreated {
	return TaskCreated{base: base{Name: "TaskCreated", At: at}, TaskId: id}
}

// TaskClaimed is raised when a station pulls a task from the pool.
type TaskClaimed struct {
	base
	TaskId    TaskId
	StationId StationId
}

func NewTaskClaimed(id TaskId, station StationId, at time.Time) TaskClaimed {
	return TaskClaimed{base: base{Name: "TaskClaimed", At: at}, TaskId: id, StationId: station}
}

// LeaseExpired is raised when an unconfirmed claim's lease times out and the
// task returns to the pool.
type LeaseExpired struct {
	base
	TaskId TaskId
}

func NewLeaseExpired(id TaskId, at time.Time) LeaseExpired {
	return LeaseExpired{base: base{Name: "LeaseExpired", At: at}, TaskId: id}
}

// TaskCompleted is raised when a station finishes a claimed task.
type TaskCompleted struct {
	base
	TaskId    TaskId
	StationId StationId
}

func NewTaskCompleted(id TaskId, station StationId, at time.Time) TaskCompleted {
	return TaskCompleted{base: base{Name: "TaskCompleted", At: at}, TaskId: id, StationId: station}
}

// ItemPicked is raised when a Pick task records an item retrieved into a tote.
type ItemPicked struct {
	base
	TaskId TaskId
}

func NewItemPicked(id TaskId, at time.Time) ItemPicked {
	return ItemPicked{base: base{Name: "ItemPicked", At: at}, TaskId: id}
}

// PackageSealed is raised when a package's contents are scanned and it is sealed.
type PackageSealed struct {
	base
	PackageId PackageId
}

func NewPackageSealed(id PackageId, at time.Time) PackageSealed {
	return PackageSealed{base: base{Name: "PackageSealed", At: at}, PackageId: id}
}

// WeightDiscrepancyDetected is raised when SLAM finds actual weight outside tolerance.
type WeightDiscrepancyDetected struct {
	base
	PackageId      PackageId
	ExpectedWeight float64
	ActualWeight   float64
}

func NewWeightDiscrepancyDetected(id PackageId, expected, actual float64, at time.Time) WeightDiscrepancyDetected {
	return WeightDiscrepancyDetected{
		base:           base{Name: "WeightDiscrepancyDetected", At: at},
		PackageId:      id,
		ExpectedWeight: expected,
		ActualWeight:   actual,
	}
}

// LabelApplied is raised when SLAM passes the weigh-check and applies the shipping label.
type LabelApplied struct {
	base
	PackageId PackageId
}

func NewLabelApplied(id PackageId, at time.Time) LabelApplied {
	return LabelApplied{base: base{Name: "LabelApplied", At: at}, PackageId: id}
}

// PackageDiverted is raised when a package fails the SLAM weigh-check and is
// routed off the standard path instead of being labeled.
type PackageDiverted struct {
	base
	PackageId PackageId
}

func NewPackageDiverted(id PackageId, at time.Time) PackageDiverted {
	return PackageDiverted{base: base{Name: "PackageDiverted", At: at}, PackageId: id}
}
