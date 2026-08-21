package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	pack "github.com/claudioed/fulfillment-execution/internal/domain/package"
	"github.com/claudioed/fulfillment-execution/internal/domain/station"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

// statusFor maps a typed domain/application error to an HTTP status code.
func statusFor(err error) int {
	switch {
	case errors.Is(err, usecases.ErrTaskNotFound),
		errors.Is(err, usecases.ErrStationNotFound),
		errors.Is(err, usecases.ErrPackageNotFound):
		return http.StatusNotFound

	case errors.Is(err, task.ErrAlreadyClaimed),
		errors.Is(err, task.ErrAlreadyCompleted),
		errors.Is(err, task.ErrNotClaimed),
		errors.Is(err, task.ErrNotOwner),
		errors.Is(err, station.ErrOccupied),
		errors.Is(err, station.ErrNotOccupied),
		errors.Is(err, pack.ErrAlreadySealed),
		errors.Is(err, pack.ErrAlreadyProcessed),
		errors.Is(err, pack.ErrNotSealed),
		errors.Is(err, usecases.ErrNoClaimableTask):
		return http.StatusConflict

	case errors.Is(err, task.ErrCapabilityMismatch),
		errors.Is(err, station.ErrCapabilityMismatch),
		errors.Is(err, pack.ErrNoScannedContents),
		errors.Is(err, usecases.ErrWrongTaskType):
		return http.StatusUnprocessableEntity

	default:
		return http.StatusInternalServerError
	}
}

func writeError(w http.ResponseWriter, err error) {
	status := statusFor(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
