package http_test

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"testing"

	"github.com/claudioed/fulfillment-execution/internal/adapters/inbound/http"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/events"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
)

// stubLocationRoleLookup is a fixed-response ports.LocationRoleLookup for
// HTTP-layer tests.
type stubLocationRoleLookup struct {
	info ports.LocationRoleInfo
}

func (s stubLocationRoleLookup) GetRole(_ context.Context, _ string) (ports.LocationRoleInfo, error) {
	return s.info, nil
}

func newTestServerWithLocationLookup(lookup ports.LocationRoleLookup) stdhttp.Handler {
	stations := memory.NewStationRepo()
	publisher := events.NewBufferedPublisher()

	h, _, _, _, _ := newTestHandlers()
	h.RegisterStation = &usecases.RegisterStation{Stations: stations, Publisher: publisher, LocationLookup: lookup}
	return http.NewRouter(h, nil)
}

func TestPostRegisterStation_WithLocationCode_WorkCenter_Accepted(t *testing.T) {
	srv := newTestServerWithLocationLookup(stubLocationRoleLookup{info: ports.LocationRoleInfo{Role: "WorkCenter", Known: true}})
	rec := doJSON(t, srv, stdhttp.MethodPost, "/stations", map[string]any{
		"stationId":    "s1",
		"capabilities": []string{"pack"},
		"locationCode": "WH1-STOR-AMB-A07-01-01-A",
	})
	if rec.Code != stdhttp.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["locationCode"] != "WH1-STOR-AMB-A07-01-01-A" {
		t.Fatalf("expected locationCode in response, got %s", rec.Body.String())
	}
}

func TestPostRegisterStation_WithLocationCode_NonWorkCenter_Returns422(t *testing.T) {
	srv := newTestServerWithLocationLookup(stubLocationRoleLookup{info: ports.LocationRoleInfo{Role: "Storage", Known: true}})
	rec := doJSON(t, srv, stdhttp.MethodPost, "/stations", map[string]any{
		"stationId":    "s1",
		"capabilities": []string{"pack"},
		"locationCode": "WH1-STOR-AMB-A07-01-01-A",
	})
	if rec.Code != stdhttp.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPostRegisterStation_NoLocationCode_OmitsFieldFromResponse(t *testing.T) {
	srv, _, _, _, _ := newTestServer()
	rec := doJSON(t, srv, stdhttp.MethodPost, "/stations", map[string]any{
		"stationId":    "s1",
		"capabilities": []string{"pick"},
	})
	if rec.Code != stdhttp.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if _, ok := resp["locationCode"]; ok {
		t.Fatalf("expected locationCode to be omitted, got %s", rec.Body.String())
	}
}
