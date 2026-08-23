package observability

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/semconv/v1.43.0/httpconv"
)

// HTTPServerMetrics returns chi middleware recording the OTel-semantic
// http.server.request.duration histogram for every request. otelchi only
// produces spans -- it has no MeterProvider option at all -- so this is the
// HTTP metrics half, not a duplicate of it.
//
// The route pattern (/tasks/{id}/complete), not the raw path, is used as the
// http.route attribute, so the metric stays low-cardinality.
func HTTPServerMetrics() func(http.Handler) http.Handler {
	duration, err := httpconv.NewServerRequestDuration(otel.Meter(InstrumentationName))
	if err != nil {
		// Instrument creation failed; fall back to a pass-through rather
		// than failing the router build over a metric.
		otel.Handle(err)
		return func(next http.Handler) http.Handler { return next }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			attrs := []attribute.KeyValue{
				duration.AttrResponseStatusCode(ww.Status()),
			}
			// RoutePattern is only populated once chi has matched, i.e.
			// after the handler ran; an unmatched request has none, and
			// omitting it is better than recording the raw path.
			if pattern := routePattern(r); pattern != "" {
				attrs = append(attrs, duration.AttrRoute(pattern))
			}

			duration.Record(r.Context(), time.Since(start).Seconds(), requestMethod(r.Method), scheme(r), attrs...)
		})
	}
}

func routePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		return rctx.RoutePattern()
	}
	return ""
}

func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// requestMethod maps a method to its semantic-convention constant, falling
// back to _OTHER so an arbitrary method string can never blow up the
// attribute's cardinality.
func requestMethod(method string) httpconv.RequestMethodAttr {
	switch method {
	case http.MethodConnect:
		return httpconv.RequestMethodConnect
	case http.MethodDelete:
		return httpconv.RequestMethodDelete
	case http.MethodGet:
		return httpconv.RequestMethodGet
	case http.MethodHead:
		return httpconv.RequestMethodHead
	case http.MethodOptions:
		return httpconv.RequestMethodOptions
	case http.MethodPatch:
		return httpconv.RequestMethodPatch
	case http.MethodPost:
		return httpconv.RequestMethodPost
	case http.MethodPut:
		return httpconv.RequestMethodPut
	case http.MethodTrace:
		return httpconv.RequestMethodTrace
	default:
		return httpconv.RequestMethodOther
	}
}
