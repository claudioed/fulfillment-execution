// Package http is the inbound REST adapter: chi handlers, DTOs, and
// domain-error-to-HTTP-status mapping. Domain structs never leak through it.
package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	pack "github.com/claudioed/fulfillment-execution/internal/domain/package"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/station"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

// Handlers wires REST endpoints to use cases.
type Handlers struct {
	CreateTask      *usecases.CreateTask
	ClaimNext       *usecases.ClaimNext
	RenewLease      *usecases.RenewLease
	CompleteTask    *usecases.CompleteTask
	SealPackage     *usecases.SealPackage
	RunSlam         *usecases.RunSlam
	GetQueueDepth   *usecases.GetQueueDepth
	ExpireLeases    *usecases.ExpireLeases
	RegisterStation *usecases.RegisterStation
}

func toTaskResponse(t *task.Task) taskResponse {
	caps := make([]string, 0, len(t.RequiredCapabilities()))
	for c := range t.RequiredCapabilities() {
		caps = append(caps, string(c))
	}
	resp := taskResponse{
		Id:                   string(t.Id()),
		Type:                 string(t.Type()),
		Status:               string(t.Status()),
		CPT:                  t.CPT().Time(),
		OrderRef:             string(t.OrderRef()),
		RequiredCapabilities: caps,
	}
	if lease := t.Lease(); lease != nil {
		stationId := string(lease.StationId)
		resp.LeaseStationId = &stationId
		expiry := lease.Expiry
		resp.LeaseExpiry = &expiry
	}
	return resp
}

func toPackageResponse(p *pack.Package) packageResponse {
	return packageResponse{
		Id:              string(p.Id()),
		OrderRef:        string(p.OrderRef()),
		Status:          string(p.Status()),
		ScannedContents: p.ScannedContents(),
	}
}

func toStationResponse(s *station.Station) stationResponse {
	caps := make([]string, 0, len(s.Capabilities()))
	for c := range s.Capabilities() {
		caps = append(caps, string(c))
	}
	return stationResponse{
		Id:           string(s.Id()),
		Capabilities: caps,
		Occupied:     s.IsOccupied(),
	}
}

// PostTask handles POST /tasks.
func (h *Handlers) PostTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequestNoInstance(w, "invalid request body")
		return
	}
	if msg := req.validate(); msg != "" {
		writeBadRequestNoInstance(w, msg)
		return
	}

	caps := make([]shared.Capability, len(req.RequiredCapabilities))
	for i, c := range req.RequiredCapabilities {
		caps[i] = shared.Capability(c)
	}

	t, err := h.CreateTask.Execute(r.Context(), task.Type(req.Type), shared.NewCPT(req.CPT), shared.OrderRef(req.OrderRef), shared.NewCapabilitySet(caps...))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCreated(w, "/tasks/"+string(t.Id()), toTaskResponse(t))
}

// PostClaimNext handles POST /stations/{stationId}/claim-next.
func (h *Handlers) PostClaimNext(w http.ResponseWriter, r *http.Request) {
	stationId := chi.URLParam(r, "stationId")
	var req claimNextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, r, "invalid request body")
		return
	}
	if msg := req.validate(); msg != "" {
		writeBadRequest(w, r, msg)
		return
	}

	t, err := h.ClaimNext.Execute(r.Context(), shared.StationId(stationId), task.Type(req.TaskType))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTaskResponse(t))
}

// PostRenewLease handles POST /tasks/{id}/renew-lease.
func (h *Handlers) PostRenewLease(w http.ResponseWriter, r *http.Request) {
	taskId := chi.URLParam(r, "id")
	var req renewLeaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, r, "invalid request body")
		return
	}
	if msg := req.validate(); msg != "" {
		writeBadRequest(w, r, msg)
		return
	}

	if err := h.RenewLease.Execute(r.Context(), shared.TaskId(taskId), shared.StationId(req.StationId)); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PostCompleteTask handles POST /tasks/{id}/complete.
func (h *Handlers) PostCompleteTask(w http.ResponseWriter, r *http.Request) {
	taskId := chi.URLParam(r, "id")
	var req completeTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, r, "invalid request body")
		return
	}
	if msg := req.validate(); msg != "" {
		writeBadRequest(w, r, msg)
		return
	}

	if err := h.CompleteTask.Execute(r.Context(), shared.TaskId(taskId), shared.StationId(req.StationId)); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PostSealPackage handles POST /tasks/{id}/seal-package.
func (h *Handlers) PostSealPackage(w http.ResponseWriter, r *http.Request) {
	taskId := chi.URLParam(r, "id")
	var req sealPackageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, r, "invalid request body")
		return
	}
	if msg := req.validate(); msg != "" {
		writeBadRequest(w, r, msg)
		return
	}

	p, err := h.SealPackage.Execute(r.Context(), shared.TaskId(taskId), shared.StationId(req.StationId), req.Contents)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCreated(w, "/packages/"+string(p.Id()), toPackageResponse(p))
}

// PostRunSlam handles POST /packages/{id}/slam.
func (h *Handlers) PostRunSlam(w http.ResponseWriter, r *http.Request) {
	packageId := chi.URLParam(r, "id")
	var req runSlamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, r, "invalid request body")
		return
	}

	if err := h.RunSlam.Execute(r.Context(), shared.PackageId(packageId), req.ActualWeight, req.ExpectedWeight); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetQueueDepthHandler handles GET /queues/{taskType}/depth.
func (h *Handlers) GetQueueDepthHandler(w http.ResponseWriter, r *http.Request) {
	taskType := chi.URLParam(r, "taskType")
	depth, err := h.GetQueueDepth.Execute(r.Context(), task.Type(taskType))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, queueDepthResponse{TaskType: taskType, Depth: depth})
}

// PostExpireLeases handles POST /tasks/expire-leases.
func (h *Handlers) PostExpireLeases(w http.ResponseWriter, r *http.Request) {
	freed, err := h.ExpireLeases.Execute(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, expireLeasesResponse{Freed: freed})
}

// PostRegisterStation handles POST /stations.
func (h *Handlers) PostRegisterStation(w http.ResponseWriter, r *http.Request) {
	var req registerStationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequestNoInstance(w, "invalid request body")
		return
	}
	if msg := req.validate(); msg != "" {
		writeBadRequestNoInstance(w, msg)
		return
	}

	s, err := h.RegisterStation.Execute(r.Context(), req.StationId, req.Capabilities)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCreated(w, "/stations/"+string(s.Id()), toStationResponse(s))
}

// GetHealthz handles GET /healthz.
func (h *Handlers) GetHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
