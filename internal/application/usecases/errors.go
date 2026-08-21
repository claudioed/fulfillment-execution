// Package usecases implements the application layer: one struct per use
// case, depending only on the domain and on ports.
package usecases

import "errors"

// ErrTaskNotFound is returned when a referenced task does not exist.
var ErrTaskNotFound = errors.New("usecases: task not found")

// ErrStationNotFound is returned when a referenced station does not exist.
var ErrStationNotFound = errors.New("usecases: station not found")

// ErrPackageNotFound is returned when a referenced package does not exist.
var ErrPackageNotFound = errors.New("usecases: package not found")

// ErrNoClaimableTask is returned by ClaimNext when the pool has no pending
// task the station is certified/equipped for.
var ErrNoClaimableTask = errors.New("usecases: no claimable task for station capabilities")

// ErrWrongTaskType is returned when a use case is invoked against a task of
// the wrong type (e.g. sealing a package against a Pick task).
var ErrWrongTaskType = errors.New("usecases: wrong task type for this operation")
