// Package observability wires this service's OpenTelemetry pipeline: a
// TracerProvider and a MeterProvider that export over OTLP/gRPC to a
// Collector, a slog handler that stamps log records with the active
// trace/span id, the Kafka header carrier that carries trace context across
// the message boundary, and the domain metrics adapter.
//
// It sits at the adapter tier -- it talks to an external system (the OTel
// Collector) the same way outbound/postgres talks to Postgres -- but is a
// cross-cutting concern used by both inbound and outbound adapters, so it
// lives beside them rather than under either.
package observability

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

const (
	// DefaultServiceName is this service's name in traces and metrics when
	// OTEL_SERVICE_NAME is not set.
	DefaultServiceName = "fulfillment-execution"

	// DefaultOTLPEndpoint is the OTel Collector's standard gRPC receiver
	// address.
	DefaultOTLPEndpoint = "localhost:4317"

	defaultServiceVersion = "dev"
	defaultEnvironment    = "local"

	// InstrumentationName is the scope every instrument created by this
	// package is attributed to.
	InstrumentationName = "github.com/claudioed/fulfillment-execution"
)

// ServiceName resolves the service name from OTEL_SERVICE_NAME, falling back
// to DefaultServiceName. Both the composition root and the HTTP router need
// it, so it is resolved here rather than threaded through both.
func ServiceName() string { return getenv("OTEL_SERVICE_NAME", DefaultServiceName) }

// ServiceVersion resolves the service version from SERVICE_VERSION. There is
// no build-time ldflags version in this module, so the env var is the only
// source.
func ServiceVersion() string { return getenv("SERVICE_VERSION", defaultServiceVersion) }

// Endpoint resolves the Collector's OTLP/gRPC address from
// OTEL_EXPORTER_OTLP_ENDPOINT.
func Endpoint() string { return getenv("OTEL_EXPORTER_OTLP_ENDPOINT", DefaultOTLPEndpoint) }

// Setup builds and installs the global TracerProvider, MeterProvider and
// trace-context propagator, and starts Go runtime metrics collection. It
// returns a shutdown func that flushes and closes both providers.
//
// Export is deliberately non-blocking: the OTLP gRPC exporters dial lazily
// and no blocking dial option is set, so an unreachable Collector degrades
// to "telemetry silently dropped", never to a service that will not start or
// requests that hang.
//
// A non-nil error means either the providers could not be built at all (in
// which case the returned shutdown func is nil) or the providers are live
// but runtime metrics collection failed to start (shutdown is non-nil and
// must still be called). Callers should therefore check the shutdown func
// for nil rather than assuming err == nil implies it is usable.
func Setup(ctx context.Context, serviceName, serviceVersion, otlpEndpoint string) (func(context.Context) error, error) {
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	if serviceVersion == "" {
		serviceVersion = defaultServiceVersion
	}
	if otlpEndpoint == "" {
		otlpEndpoint = DefaultOTLPEndpoint
	}

	endpointURL := normalizeEndpoint(otlpEndpoint)

	res, err := newResource(serviceName, serviceVersion)
	if err != nil {
		return nil, err
	}

	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpointURL(endpointURL),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExporter),
	)

	metricExporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpointURL(endpointURL),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		// The tracer provider is already live; tear it down rather than
		// leaking its batch processor goroutine.
		_ = tracerProvider.Shutdown(ctx)
		return nil, err
	}
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
	)

	// Route the SDK's own failures (a failed export, a bad instrument) into
	// the structured logger instead of the stdlib default, so telemetry
	// problems are as greppable as everything else this service logs.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Warn("opentelemetry error", "error", err)
	}))

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Go runtime metrics (goroutines, GC, memory) via the OTel-native
	// collector rather than hand-rolled runtime stats.
	runtimeErr := runtime.Start(runtime.WithMeterProvider(meterProvider))

	shutdown := func(ctx context.Context) error {
		return errors.Join(
			tracerProvider.Shutdown(ctx),
			meterProvider.Shutdown(ctx),
		)
	}
	return shutdown, runtimeErr
}

// normalizeEndpoint turns this platform's host:port convention into the URL
// form the OTLP exporters expect, leaving a value that already carries a
// scheme alone.
//
// It also rewrites OTEL_EXPORTER_OTLP_ENDPOINT in place when that variable
// holds a bare host:port. The exporters read that variable themselves before
// applying the options above, and a non-URL value makes them log a
// plain-text parse error at every startup -- which would break this
// service's JSON-only log contract -- even though the explicit option still
// wins. Normalizing once, here, keeps both readings in agreement.
func normalizeEndpoint(endpoint string) string {
	url := endpoint
	if !strings.Contains(url, "://") {
		// Every exporter here is constructed WithInsecure, so plain http is
		// the scheme that matches.
		url = "http://" + url
	}
	if raw := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); raw != "" && !strings.Contains(raw, "://") {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://"+raw)
	}
	return url
}

// newResource describes this service to the Collector. The keys come from
// the semantic conventions package, not hand-typed strings.
func newResource(serviceName, serviceVersion string) (*resource.Resource, error) {
	return resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
			semconv.DeploymentEnvironmentNameKey.String(getenv("ENVIRONMENT", defaultEnvironment)),
		),
	)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
