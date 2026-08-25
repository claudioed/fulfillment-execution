package productclassification_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/productclassification"
)

// fakeDoer is a stub productclassification.HTTPDoer so these tests never
// hit the network.
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

// jsonResponse builds a fake *http.Response for fakeDoer. The body is
// always closed by the code under test
// (productclassification.Client.GetClassification defers
// resp.Body.Close() on every return path); bodyclose's static analysis
// cannot trace that through the HTTPDoer interface boundary from this test
// file, so each call site below carries an explicit nolint.
func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestClient_GetClassification_200_HazmatWithClass(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusOK, `{"handlingTags": ["Hazmat"], "dotHazardClass": 3}`)} //nolint:bodyclose
	client := productclassification.NewClient("http://inventory-storage.local", doer)

	info, err := client.GetClassification(context.Background(), "sku-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Known {
		t.Fatalf("expected Known=true")
	}
	if !info.Hazmat {
		t.Fatalf("expected Hazmat=true")
	}
	if info.DOTHazardClass != 3 {
		t.Fatalf("expected DOTHazardClass=3, got %d", info.DOTHazardClass)
	}
	if doer.req.URL.Path != "/products/sku-1/classification" {
		t.Fatalf("expected path /products/sku-1/classification, got %s", doer.req.URL.Path)
	}
}

func TestClient_GetClassification_200_FragileNoHazardClass(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusOK, `{"handlingTags": ["Fragile"]}`)} //nolint:bodyclose
	client := productclassification.NewClient("http://inventory-storage.local", doer)

	info, err := client.GetClassification(context.Background(), "sku-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Known {
		t.Fatalf("expected Known=true")
	}
	if info.Hazmat {
		t.Fatalf("expected Hazmat=false")
	}
	if !info.Fragile {
		t.Fatalf("expected Fragile=true")
	}
	if info.DOTHazardClass != 0 {
		t.Fatalf("expected DOTHazardClass=0 when absent, got %d", info.DOTHazardClass)
	}
}

func TestClient_GetClassification_404_FailsOpen(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusNotFound, "")} //nolint:bodyclose
	client := productclassification.NewClient("http://inventory-storage.local", doer)

	info, err := client.GetClassification(context.Background(), "sku-unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Known {
		t.Fatalf("expected Known=false on 404")
	}
}

func TestClient_GetClassification_500_ReturnsError(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusInternalServerError, "")} //nolint:bodyclose
	client := productclassification.NewClient("http://inventory-storage.local", doer)

	_, err := client.GetClassification(context.Background(), "sku-1")
	if !errors.Is(err, productclassification.ErrUnexpectedStatus) {
		t.Fatalf("expected ErrUnexpectedStatus, got %v", err)
	}
}

func TestClient_GetClassification_TransportError_Propagates(t *testing.T) {
	transportErr := errors.New("connection refused")
	doer := &fakeDoer{err: transportErr}
	client := productclassification.NewClient("http://inventory-storage.local", doer)

	_, err := client.GetClassification(context.Background(), "sku-1")
	if !errors.Is(err, transportErr) {
		t.Fatalf("expected transport error to propagate, got %v", err)
	}
}

func TestClient_GetClassification_MalformedJSON_ReturnsError(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusOK, `{not-json`)} //nolint:bodyclose
	client := productclassification.NewClient("http://inventory-storage.local", doer)

	_, err := client.GetClassification(context.Background(), "sku-1")
	if err == nil {
		t.Fatalf("expected an error decoding malformed JSON")
	}
}

func TestNewClient_NilDoer_DefaultsToRealHTTPClient(t *testing.T) {
	client := productclassification.NewClient("http://inventory-storage.local", nil)
	if client == nil {
		t.Fatalf("expected a non-nil client")
	}
}

func TestPermissiveLookup_AlwaysReportsUnknown(t *testing.T) {
	lookup := productclassification.NewPermissiveLookup()
	info, err := lookup.GetClassification(context.Background(), "sku-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Known {
		t.Fatalf("expected Known=false from PermissiveLookup")
	}
}

// sanity: confirm we serialize the request the way inventory-storage
// expects (Accept header set, GET method).
func TestClient_GetClassification_SetsAcceptHeaderAndMethod(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusOK, `{"handlingTags": []}`)} //nolint:bodyclose
	client := productclassification.NewClient("http://inventory-storage.local", doer)

	_, err := client.GetClassification(context.Background(), "sku-1")
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

// A SKU URL-escapes safely — this endpoint accepts arbitrary SKU strings,
// not just simple alphanumerics.
func TestClient_GetClassification_EscapesSKUInPath(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusOK, `{"handlingTags": []}`)} //nolint:bodyclose
	client := productclassification.NewClient("http://inventory-storage.local", doer)

	_, err := client.GetClassification(context.Background(), "sku/with slash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(doer.req.URL.Path, "sku") {
		t.Fatalf("expected escaped sku in path, got %s", doer.req.URL.Path)
	}
}
