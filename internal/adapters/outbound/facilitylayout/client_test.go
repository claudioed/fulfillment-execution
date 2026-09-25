package facilitylayout_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/facilitylayout"
)

// fakeDoer is a stub facilitylayout.HTTPDoer so these tests never hit the
// network.
type fakeDoer struct {
	resp *http.Response
	err  error
	req  *http.Request
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.req = req
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestClient_GetRole_200_WorkCenter(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusOK, `{"role":"WorkCenter"}`)} //nolint:bodyclose
	client := facilitylayout.NewClient("http://facility-layout.local", doer)

	info, err := client.GetRole(context.Background(), "WH1-STOR-AMB-A07-01-01-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Known {
		t.Fatalf("expected Known=true")
	}
	if info.Role != "WorkCenter" {
		t.Fatalf("expected Role=WorkCenter, got %s", info.Role)
	}
	if doer.req.URL.Path != "/locations/WH1-STOR-AMB-A07-01-01-A" {
		t.Fatalf("expected path /locations/WH1-STOR-AMB-A07-01-01-A, got %s", doer.req.URL.Path)
	}
}

func TestClient_GetRole_200_Storage(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusOK, `{"role":"Storage"}`)} //nolint:bodyclose
	client := facilitylayout.NewClient("http://facility-layout.local", doer)

	info, err := client.GetRole(context.Background(), "WH1-STOR-AMB-A07-01-01-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Known || info.Role != "Storage" {
		t.Fatalf("expected Known=true, Role=Storage, got %+v", info)
	}
}

func TestClient_GetRole_404_FailsOpen(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusNotFound, "")} //nolint:bodyclose
	client := facilitylayout.NewClient("http://facility-layout.local", doer)

	info, err := client.GetRole(context.Background(), "NOPE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Known {
		t.Fatalf("expected Known=false on 404")
	}
}

func TestClient_GetRole_400_FailsOpen(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusBadRequest, "")} //nolint:bodyclose
	client := facilitylayout.NewClient("http://facility-layout.local", doer)

	info, err := client.GetRole(context.Background(), "not-seven-segments")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Known {
		t.Fatalf("expected Known=false on 400")
	}
}

func TestClient_GetRole_500_ReturnsError(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusInternalServerError, "")} //nolint:bodyclose
	client := facilitylayout.NewClient("http://facility-layout.local", doer)

	_, err := client.GetRole(context.Background(), "WH1-STOR-AMB-A07-01-01-A")
	if !errors.Is(err, facilitylayout.ErrUnexpectedStatus) {
		t.Fatalf("expected ErrUnexpectedStatus, got %v", err)
	}
}

func TestClient_GetRole_TransportError_Propagates(t *testing.T) {
	transportErr := errors.New("connection refused")
	doer := &fakeDoer{err: transportErr}
	client := facilitylayout.NewClient("http://facility-layout.local", doer)

	_, err := client.GetRole(context.Background(), "WH1-STOR-AMB-A07-01-01-A")
	if !errors.Is(err, transportErr) {
		t.Fatalf("expected transport error to propagate, got %v", err)
	}
}

func TestClient_GetRole_MalformedJSON_ReturnsError(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusOK, `{not-json`)} //nolint:bodyclose
	client := facilitylayout.NewClient("http://facility-layout.local", doer)

	_, err := client.GetRole(context.Background(), "WH1-STOR-AMB-A07-01-01-A")
	if err == nil {
		t.Fatalf("expected an error decoding malformed JSON")
	}
}

func TestNewClient_NilDoer_DefaultsToRealHTTPClient(t *testing.T) {
	client := facilitylayout.NewClient("http://facility-layout.local", nil)
	if client == nil {
		t.Fatalf("expected a non-nil client")
	}
}

func TestPermissiveLookup_AlwaysReportsUnknown(t *testing.T) {
	lookup := facilitylayout.NewPermissiveLookup()
	info, err := lookup.GetRole(context.Background(), "WH1-STOR-AMB-A07-01-01-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Known {
		t.Fatalf("expected Known=false from PermissiveLookup")
	}
}

// sanity: confirm we serialize the request the way facility-layout expects
// (Accept header set, GET method).
func TestClient_GetRole_SetsAcceptHeaderAndMethod(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusOK, `{"role":"WorkCenter"}`)} //nolint:bodyclose
	client := facilitylayout.NewClient("http://facility-layout.local", doer)

	_, err := client.GetRole(context.Background(), "WH1-STOR-AMB-A07-01-01-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doer.req.Method != http.MethodGet {
		t.Fatalf("expected GET, got %s", doer.req.Method)
	}
	if doer.req.Header.Get("Accept") != "application/json" {
		t.Fatalf("expected Accept: application/json header")
	}
}
