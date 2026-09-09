// Package productclassification provides outbound
// ports.ProductClassificationLookup implementations: an HTTP client that
// calls inventory-storage's GET /products/{sku}/classification endpoint,
// and a permissive no-op used by default so existing tests, CI and
// deployments are unaffected. This mirrors inventory-storage's own
// internal/adapters/outbound/facilitylayout package pattern exactly
// (permissive-by-default, env-var-selected) — see ADR-0010.
package productclassification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
)

// DefaultTimeout bounds a single classification lookup request, so a slow
// or hanging inventory-storage does not stall SealPackage indefinitely.
const DefaultTimeout = 5 * time.Second

// ErrUnexpectedStatus wraps an inventory-storage response status this
// client does not have specific handling for (anything other than 200 or
// 404).
var ErrUnexpectedStatus = errors.New("inventory-storage: unexpected response status")

// HTTPDoer is the subset of *http.Client this adapter depends on, so unit
// tests can substitute a fake transport without a real server.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client is a plain net/http implementation of
// ports.ProductClassificationLookup, calling inventory-storage's
// GET /products/{sku}/classification.
type Client struct {
	baseURL     string
	doer        HTTPDoer
	bearerToken string
}

// Option customises a Client.
type Option func(*Client)

// WithBearerToken makes every request carry "Authorization: Bearer <token>"
// (fleet REST identity, ADR-0021; env INVENTORY_STORAGE_API_KEY). An empty
// token sends no header, so an unconfigured key is indistinguishable from
// the pre-auth behaviour.
func WithBearerToken(token string) Option {
	return func(c *Client) { c.bearerToken = token }
}

// NewClient builds a Client against baseURL (e.g. from
// INVENTORY_STORAGE_BASE_URL). A nil doer defaults to an *http.Client with
// DefaultTimeout.
func NewClient(baseURL string, doer HTTPDoer, opts ...Option) *Client {
	if doer == nil {
		doer = &http.Client{Timeout: DefaultTimeout}
	}
	c := &Client{baseURL: strings.TrimRight(baseURL, "/"), doer: doer}
	for _, o := range opts {
		o(c)
	}
	return c
}

// classificationResponse mirrors inventory-storage's
// productClassificationResponse DTO, plus the new optional dotHazardClass
// field (1-9) added there in a parallel change.
type classificationResponse struct {
	HandlingTags     []string `json:"handlingTags"`
	TemperatureClass string   `json:"temperatureClass"`
	DOTHazardClass   *int     `json:"dotHazardClass"`
}

// GetClassification calls inventory-storage's product-classification
// endpoint for sku.
//
//   - A 404 is treated as Known=false (fail-open): that SKU is not
//     classified in inventory-storage.
//   - Any transport error or non-2xx/404 status returns an error. Per
//     ADR-0010, SealPackage fails open for a single SKU's lookup error
//     (treats it as unclassified) rather than failing the whole seal — a
//     pack-time classification blip should not halt an active pack
//     station over a soft dependency. This differs deliberately from
//     inventory-storage's own StowStock, which fails closed for a
//     classified SKU's placement-lookup error: that is a harder,
//     at-rest safety gate, not a live pack-time hint.
func (c *Client) GetClassification(ctx context.Context, sku string) (ports.ClassificationInfo, error) {
	endpoint := fmt.Sprintf("%s/products/%s/classification", c.baseURL, url.PathEscape(sku))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ports.ClassificationInfo{}, err
	}
	req.Header.Set("Accept", "application/json")
	if c.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	}

	resp, err := c.doer.Do(req)
	if err != nil {
		return ports.ClassificationInfo{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		var body classificationResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return ports.ClassificationInfo{}, err
		}
		info := ports.ClassificationInfo{Known: true}
		for _, tag := range body.HandlingTags {
			switch tag {
			case "Hazmat":
				info.Hazmat = true
			case "Fragile":
				info.Fragile = true
			}
		}
		if body.DOTHazardClass != nil {
			info.DOTHazardClass = *body.DOTHazardClass
		}
		return info, nil
	case http.StatusNotFound:
		return ports.ClassificationInfo{Known: false}, nil
	default:
		return ports.ClassificationInfo{}, fmt.Errorf("%w: %d", ErrUnexpectedStatus, resp.StatusCode)
	}
}
