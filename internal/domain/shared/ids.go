// Package shared holds value objects and domain events shared across the
// task, station, and package aggregates.
package shared

// TaskId identifies a Task aggregate.
type TaskId string

// StationId identifies a Station aggregate.
type StationId string

// PackageId identifies a Package aggregate.
type PackageId string

// OrderRef identifies the released work this task or package fulfills.
type OrderRef string
