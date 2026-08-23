package observability_test

import (
	"context"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/claudioed/fulfillment-execution/internal/observability"
)

// TestSetup_DoesNotBlockOrFailWithoutACollector is the concrete form of the
// non-blocking-export requirement: nothing is listening on the endpoint, and
// Setup must still return promptly with usable providers rather than
// erroring or hanging on a dial.
func TestSetup_DoesNotBlockOrFailWithoutACollector(t *testing.T) {
	// Port 1 on loopback: reserved, and nothing will ever accept there.
	const unreachable = "127.0.0.1:1"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	var (
		shutdown func(context.Context) error
		err      error
	)
	go func() {
		defer close(done)
		shutdown, err = observability.Setup(ctx, "fulfillment-execution-test", "v0.0.0-test", unreachable)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Setup blocked for 5s with no collector reachable; the exporter must dial lazily")
	}

	if err != nil {
		t.Fatalf("Setup returned an error with no collector running: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Setup returned a nil shutdown func")
	}

	// Recording a span against the now-global provider must also not block
	// or panic even though the export will never land anywhere.
	_, span := otel.Tracer("test").Start(ctx, "smoke")
	span.End()

	// Shutdown flushes the span recorded above, which can never land, so
	// what matters is that it stops at its context's deadline instead of
	// retrying forever -- the bound main relies on during graceful shutdown.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- shutdown(shutdownCtx) }()

	select {
	case <-shutdownDone:
		// An export error here is acceptable and expected; hanging is not.
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown ignored its context deadline with no collector reachable")
	}
}

func TestSetup_DefaultsEmptyArguments(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdown, err := observability.Setup(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Setup with empty arguments: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Setup returned a nil shutdown func")
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(shutdownCtx)
	})
}

func TestServiceNameAndVersionDefaults(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("SERVICE_VERSION", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	if got := observability.ServiceName(); got != observability.DefaultServiceName {
		t.Errorf("ServiceName() = %q, want %q", got, observability.DefaultServiceName)
	}
	if got := observability.ServiceVersion(); got != "dev" {
		t.Errorf("ServiceVersion() = %q, want %q", got, "dev")
	}
	if got := observability.Endpoint(); got != observability.DefaultOTLPEndpoint {
		t.Errorf("Endpoint() = %q, want %q", got, observability.DefaultOTLPEndpoint)
	}
}

func TestServiceNameAndVersionReadEnv(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "custom-name")
	t.Setenv("SERVICE_VERSION", "1.2.3")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector:4317")

	if got := observability.ServiceName(); got != "custom-name" {
		t.Errorf("ServiceName() = %q, want %q", got, "custom-name")
	}
	if got := observability.ServiceVersion(); got != "1.2.3" {
		t.Errorf("ServiceVersion() = %q, want %q", got, "1.2.3")
	}
	if got := observability.Endpoint(); got != "collector:4317" {
		t.Errorf("Endpoint() = %q, want %q", got, "collector:4317")
	}
}

// TestSetup_NormalizesABareHostPortEndpoint guards the log-parity trap: the
// OTLP spec reads OTEL_EXPORTER_OTLP_ENDPOINT as a URL, this platform writes
// it as host:port, and an un-normalized value makes the SDK print a
// plain-text parse error into an otherwise JSON-only log stream.
func TestSetup_NormalizesABareHostPortEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "127.0.0.1:4999")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdown, err := observability.Setup(ctx, "fulfillment-execution-test", "v0.0.0-test", "127.0.0.1:4999")
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = shutdown(shutdownCtx)
	})

	if got := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); got != "http://127.0.0.1:4999" {
		t.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT = %q, want it normalized to a URL", got)
	}
}

// TestSetup_LeavesAURLEndpointAlone is the other half: a value that already
// carries a scheme must not be rewritten.
func TestSetup_LeavesAURLEndpointAlone(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4317")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdown, err := observability.Setup(ctx, "fulfillment-execution-test", "v0.0.0-test", "http://collector:4317")
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = shutdown(shutdownCtx)
	})

	if got := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); got != "http://collector:4317" {
		t.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT = %q, want it left as-is", got)
	}
}
