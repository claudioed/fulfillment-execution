package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/adapters/inbound/http"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/events"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/station"
)

func newTestServer() (stdhttp.Handler, *memory.TaskRepo, *memory.StationRepo, *memory.PackageRepo, *memory.FixedClock) {
	tasks := memory.NewTaskRepo()
	stations := memory.NewStationRepo()
	packages := memory.NewPackageRepo()
	publisher := events.NewBufferedPublisher()
	clock := memory.NewFixedClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))

	n := 0
	newTaskId := func() shared.TaskId {
		n++
		return shared.TaskId([]byte{'t', byte('0' + n)})
	}
	newPackageId := func() shared.PackageId { return shared.PackageId("pkg-1") }

	h := &http.Handlers{
		CreateTask:      &usecases.CreateTask{Tasks: tasks, Publisher: publisher, Clock: clock, NewId: newTaskId},
		ClaimNext:       &usecases.ClaimNext{Tasks: tasks, Stations: stations, Publisher: publisher, Clock: clock},
		RenewLease:      &usecases.RenewLease{Tasks: tasks, Clock: clock},
		CompleteTask:    &usecases.CompleteTask{Tasks: tasks, Publisher: publisher, Clock: clock},
		SealPackage:     &usecases.SealPackage{Tasks: tasks, Packages: packages, Publisher: publisher, Clock: clock, NewId: newPackageId},
		RunSlam:         &usecases.RunSlam{Packages: packages, Publisher: publisher, Clock: clock},
		GetQueueDepth:   &usecases.GetQueueDepth{Tasks: tasks},
		ExpireLeases:    &usecases.ExpireLeases{Tasks: tasks, Publisher: publisher, Clock: clock},
		RegisterStation: &usecases.RegisterStation{Stations: stations, Publisher: publisher},
	}
	return http.NewRouter(h, nil), tasks, stations, packages, clock
}

func doJSON(t *testing.T, srv stdhttp.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestGetHealthz(t *testing.T) {
	srv, _, _, _, _ := newTestServer()
	rec := doJSON(t, srv, stdhttp.MethodGet, "/healthz", nil)
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestPostTask_CreatesTaskInPool(t *testing.T) {
	srv, _, _, _, _ := newTestServer()
	rec := doJSON(t, srv, stdhttp.MethodPost, "/tasks", map[string]any{
		"type":                 "PICK",
		"cpt":                  time.Now().Add(time.Hour),
		"orderRef":             "order-1",
		"requiredCapabilities": []string{"pick"},
	})
	if rec.Code != stdhttp.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Id string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&created)
	if got, want := rec.Header().Get("Location"), "/tasks/"+created.Id; got != want {
		t.Fatalf("expected Location %q, got %q", want, got)
	}
}

func TestPostTask_MissingRequiredField_Returns400(t *testing.T) {
	srv, _, _, _, _ := newTestServer()
	rec := doJSON(t, srv, stdhttp.MethodPost, "/tasks", map[string]any{
		"cpt":                  time.Now().Add(time.Hour),
		"orderRef":             "order-1",
		"requiredCapabilities": []string{"pick"},
	})
	if rec.Code != stdhttp.StatusBadRequest {
		t.Fatalf("expected 400 for missing type, got %d: %s", rec.Code, rec.Body.String())
	}
	// POST /tasks has no path segment identifying a resource, so instance
	// is omitted per REST_API_TASK.md's Stage 2 instance rule.
	assertProblemDetails(t, rec, stdhttp.StatusBadRequest,
		"https://errors.fulfillment-execution.warehouse-systems.dev/invalid-request", "")
}

func TestPostClaimNext_LeasesTaskToStation(t *testing.T) {
	srv, _, stations, _, clock := newTestServer()
	_ = stations.Save(context.TODO(), station.New("s1", shared.NewCapabilitySet("pick")))
	doJSON(t, srv, stdhttp.MethodPost, "/tasks", map[string]any{
		"type": "PICK", "cpt": clock.Now().Add(time.Hour), "orderRef": "order-1", "requiredCapabilities": []string{"pick"},
	})

	rec := doJSON(t, srv, stdhttp.MethodPost, "/stations/s1/claim-next", map[string]any{"taskType": "PICK"})
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPostClaimNext_NoWorkReturns409(t *testing.T) {
	srv, _, stations, _, _ := newTestServer()
	_ = stations.Save(context.TODO(), station.New("s1", shared.NewCapabilitySet("pick")))

	rec := doJSON(t, srv, stdhttp.MethodPost, "/stations/s1/claim-next", map[string]any{"taskType": "PICK"})
	if rec.Code != stdhttp.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	assertProblemDetails(t, rec, stdhttp.StatusConflict,
		"https://errors.fulfillment-execution.warehouse-systems.dev/no-claimable-task",
		"/stations/s1/claim-next")
}

func TestFullPickLifecycle_ClaimThenComplete(t *testing.T) {
	srv, _, stations, _, clock := newTestServer()
	_ = stations.Save(context.TODO(), station.New("s1", shared.NewCapabilitySet("pick")))
	doJSON(t, srv, stdhttp.MethodPost, "/tasks", map[string]any{
		"type": "PICK", "cpt": clock.Now().Add(time.Hour), "orderRef": "order-1", "requiredCapabilities": []string{"pick"},
	})

	rec := doJSON(t, srv, stdhttp.MethodPost, "/stations/s1/claim-next", map[string]any{"taskType": "PICK"})
	var claimed struct {
		Id string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&claimed)

	completeRec := doJSON(t, srv, stdhttp.MethodPost, "/tasks/"+claimed.Id+"/complete", map[string]any{"stationId": "s1"})
	if completeRec.Code != stdhttp.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", completeRec.Code, completeRec.Body.String())
	}

	// No double-complete via the API.
	secondRec := doJSON(t, srv, stdhttp.MethodPost, "/tasks/"+claimed.Id+"/complete", map[string]any{"stationId": "s1"})
	if secondRec.Code != stdhttp.StatusConflict {
		t.Fatalf("expected 409 on double-complete, got %d", secondRec.Code)
	}
	assertProblemDetails(t, secondRec, stdhttp.StatusConflict,
		"https://errors.fulfillment-execution.warehouse-systems.dev/task-already-completed",
		"/tasks/"+claimed.Id+"/complete")
}

func TestPackAndSlamLifecycle(t *testing.T) {
	srv, _, stations, _, clock := newTestServer()
	_ = stations.Save(context.TODO(), station.New("s1", shared.NewCapabilitySet("pack")))
	doJSON(t, srv, stdhttp.MethodPost, "/tasks", map[string]any{
		"type": "PACK", "cpt": clock.Now().Add(time.Hour), "orderRef": "order-1", "requiredCapabilities": []string{"pack"},
	})

	claimRec := doJSON(t, srv, stdhttp.MethodPost, "/stations/s1/claim-next", map[string]any{"taskType": "PACK"})
	var claimed struct {
		Id string `json:"id"`
	}
	_ = json.NewDecoder(claimRec.Body).Decode(&claimed)

	sealRec := doJSON(t, srv, stdhttp.MethodPost, "/tasks/"+claimed.Id+"/seal-package", map[string]any{
		"stationId": "s1", "contents": []string{"sku-1"},
	})
	if sealRec.Code != stdhttp.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", sealRec.Code, sealRec.Body.String())
	}
	var sealed struct {
		Id string `json:"id"`
	}
	_ = json.NewDecoder(sealRec.Body).Decode(&sealed)
	if got, want := sealRec.Header().Get("Location"), "/packages/"+sealed.Id; got != want {
		t.Fatalf("expected Location %q, got %q", want, got)
	}

	slamRec := doJSON(t, srv, stdhttp.MethodPost, "/packages/"+sealed.Id+"/slam", map[string]any{
		"actualWeight": 2.0, "expectedWeight": 2.0,
	})
	if slamRec.Code != stdhttp.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", slamRec.Code, slamRec.Body.String())
	}
}

func TestGetQueueDepth(t *testing.T) {
	srv, _, _, _, clock := newTestServer()
	doJSON(t, srv, stdhttp.MethodPost, "/tasks", map[string]any{
		"type": "PICK", "cpt": clock.Now().Add(time.Hour), "orderRef": "order-1", "requiredCapabilities": []string{"pick"},
	})

	rec := doJSON(t, srv, stdhttp.MethodGet, "/queues/PICK/depth", nil)
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Depth int `json:"depth"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Depth != 1 {
		t.Fatalf("expected depth 1, got %d", resp.Depth)
	}
}

func TestPostExpireLeases(t *testing.T) {
	srv, _, stations, _, clock := newTestServer()
	_ = stations.Save(context.TODO(), station.New("s1", shared.NewCapabilitySet("pick")))
	doJSON(t, srv, stdhttp.MethodPost, "/tasks", map[string]any{
		"type": "PICK", "cpt": clock.Now().Add(time.Hour), "orderRef": "order-1", "requiredCapabilities": []string{"pick"},
	})
	doJSON(t, srv, stdhttp.MethodPost, "/stations/s1/claim-next", map[string]any{"taskType": "PICK"})

	clock.Advance(usecases.DefaultLeaseDuration + time.Minute)

	rec := doJSON(t, srv, stdhttp.MethodPost, "/tasks/expire-leases", nil)
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Freed int `json:"freed"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Freed != 1 {
		t.Fatalf("expected 1 freed task, got %d", resp.Freed)
	}
}

func TestPostRenewLease_NotFoundOnUnknownTask(t *testing.T) {
	srv, _, _, _, _ := newTestServer()
	rec := doJSON(t, srv, stdhttp.MethodPost, "/tasks/does-not-exist/renew-lease", map[string]any{"stationId": "s1"})
	if rec.Code != stdhttp.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	assertProblemDetails(t, rec, stdhttp.StatusNotFound,
		"https://errors.fulfillment-execution.warehouse-systems.dev/task-not-found",
		"/tasks/does-not-exist/renew-lease")
}

// assertProblemDetails checks rec's body against the RFC 7807 Problem
// Details shape (type, title, status, detail, instance) and confirms
// Content-Type is application/problem+json (not application/json).
func assertProblemDetails(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantType, wantInstance string) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("expected Content-Type application/problem+json, got %q", ct)
	}
	var body struct {
		Type     string `json:"type"`
		Title    string `json:"title"`
		Status   int    `json:"status"`
		Detail   string `json:"detail"`
		Instance string `json:"instance"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal problem body: %v", err)
	}
	if body.Type != wantType {
		t.Fatalf("expected type %q, got %q", wantType, body.Type)
	}
	if body.Title == "" {
		t.Fatalf("expected a non-empty title")
	}
	if body.Status != wantStatus {
		t.Fatalf("expected status %d in body, got %d", wantStatus, body.Status)
	}
	if body.Detail == "" {
		t.Fatalf("expected a non-empty detail")
	}
	if body.Instance != wantInstance {
		t.Fatalf("expected instance %q, got %q", wantInstance, body.Instance)
	}
}

func TestPostRegisterStation_CreatesStation(t *testing.T) {
	srv, _, stations, _, _ := newTestServer()
	rec := doJSON(t, srv, stdhttp.MethodPost, "/stations", map[string]any{
		"stationId":    "s1",
		"capabilities": []string{"pick"},
	})
	if rec.Code != stdhttp.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Id           string   `json:"id"`
		Capabilities []string `json:"capabilities"`
		Occupied     bool     `json:"occupied"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Id != "s1" || resp.Occupied || len(resp.Capabilities) != 1 || resp.Capabilities[0] != "pick" {
		t.Fatalf("unexpected response body: %+v", resp)
	}
	if got, want := rec.Header().Get("Location"), "/stations/s1"; got != want {
		t.Fatalf("expected Location %q, got %q", want, got)
	}

	found, _ := stations.FindById(context.TODO(), "s1")
	if found == nil {
		t.Fatalf("expected station to be persisted")
	}
}

func TestPostRegisterStation_MissingStationId_Returns400(t *testing.T) {
	srv, _, _, _, _ := newTestServer()
	rec := doJSON(t, srv, stdhttp.MethodPost, "/stations", map[string]any{
		"capabilities": []string{"pick"},
	})
	if rec.Code != stdhttp.StatusBadRequest {
		t.Fatalf("expected 400 for missing stationId, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPostAdminExpireLeases_OldRoute_NoLongerExists(t *testing.T) {
	srv, _, _, _, _ := newTestServer()
	rec := doJSON(t, srv, stdhttp.MethodPost, "/admin/expire-leases", nil)
	if rec.Code != stdhttp.StatusNotFound {
		t.Fatalf("expected the old /admin/expire-leases route to be gone (404), got %d", rec.Code)
	}
}

// Proves the gap this task closes: a freshly registered station can
// immediately be handed a task via claim-next over HTTP, where previously
// there was no way to create the station at all and claim-next always 404'd.
func TestPostRegisterStation_ThenClaimNextSucceeds(t *testing.T) {
	srv, _, _, _, clock := newTestServer()

	rec := doJSON(t, srv, stdhttp.MethodPost, "/stations", map[string]any{
		"stationId":    "s1",
		"capabilities": []string{"pick"},
	})
	if rec.Code != stdhttp.StatusCreated {
		t.Fatalf("expected 201 registering station, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, srv, stdhttp.MethodPost, "/tasks", map[string]any{
		"type": "PICK", "cpt": clock.Now().Add(time.Hour), "orderRef": "order-1", "requiredCapabilities": []string{"pick"},
	})
	if rec.Code != stdhttp.StatusCreated {
		t.Fatalf("expected 201 creating task, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, srv, stdhttp.MethodPost, "/stations/s1/claim-next", map[string]any{"taskType": "PICK"})
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200 claiming against freshly registered station, got %d: %s", rec.Code, rec.Body.String())
	}
}
