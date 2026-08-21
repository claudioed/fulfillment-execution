package shared

import "time"

// CPT is the Critical Pull Time: the deadline by which a task must be
// completed for its order to ship on schedule. Task priority derives from
// CPT — the earliest CPT is dispatched first.
type CPT struct {
	at time.Time
}

// NewCPT constructs a CPT from a deadline.
func NewCPT(at time.Time) CPT {
	return CPT{at: at}
}

// Time returns the underlying deadline.
func (c CPT) Time() time.Time {
	return c.at
}

// Before reports whether c is earlier (higher priority) than other.
func (c CPT) Before(other CPT) bool {
	return c.at.Before(other.at)
}
