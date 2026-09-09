package http

import (
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/riandyrn/otelchi"

	"github.com/claudioed/fulfillment-execution/internal/adapters/inbound/auth"
	"github.com/claudioed/fulfillment-execution/internal/observability"
)

// ProblemBase is the RFC 7807 problem-type base URL of this service; the
// auth middleware's 401/403 problem bodies use it so they look exactly like
// every other problem this adapter emits (ADR-0005).
const ProblemBase = "https://errors.fulfillment-execution.warehouse-systems.dev/"

// RouterOption customises NewRouter / NewReportsRouter.
type RouterOption func(*routerConfig)

type routerConfig struct {
	authn auth.Authenticator
	mode  auth.Mode
}

// WithAuth mounts the fleet-standard REST auth middleware (ADR-0021) on every
// route except /healthz. Without this option the router runs in auth.ModeOff,
// which is what the handler tests and a key-less local run rely on.
func WithAuth(authn auth.Authenticator, mode auth.Mode) RouterOption {
	return func(c *routerConfig) {
		c.authn = authn
		c.mode = mode
	}
}

// authMiddleware builds the middleware for the configured auth mode. Required
// overrides the default method-based policy (nil = GET/HEAD/OPTIONS need
// read, everything else read-write).
func (c routerConfig) authMiddleware(logger *slog.Logger, required func(*http.Request) auth.Scope) func(http.Handler) http.Handler {
	if c.authn == nil || c.mode == "" {
		return auth.Middleware{Mode: auth.ModeOff}.Handler
	}
	return auth.Middleware{
		Authn:       c.authn,
		Mode:        c.mode,
		Logger:      logger,
		ProblemBase: ProblemBase,
		Required:    required,
	}.Handler
}

// NewRouter builds the chi router for every Fulfillment Execution endpoint.
// A nil logger falls back to slog.Default() rather than panicking. GET
// /healthz is always open; every other route sits behind the auth middleware
// when WithAuth is supplied.
func NewRouter(h *Handlers, logger *slog.Logger, opts ...RouterOption) *chi.Mux {
	if logger == nil {
		logger = slog.Default()
	}
	var cfg routerConfig
	for _, o := range opts {
		o(&cfg)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// otelchi runs before the logger so every log line emitted downstream
	// sits inside the request span and picks up its trace_id/span_id.
	// WithChiRoutes makes the span name the route pattern
	// (POST /tasks/{id}/complete) rather than the raw path, keeping span
	// names low-cardinality.
	r.Use(otelchi.Middleware(
		observability.ServiceName(),
		otelchi.WithChiRoutes(r),
		otelchi.WithRequestMethodInSpanName(true),
	))
	r.Use(observability.HTTPServerMetrics())
	r.Use(RequestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware())

	r.Get("/healthz", h.GetHealthz)

	// Everything but the probe endpoint is inside the auth group so /healthz
	// never needs a bearer key (Kubernetes probes, fleet convention).
	r.Group(func(r chi.Router) {
		r.Use(cfg.authMiddleware(logger, nil))

		r.Post("/stations", h.PostRegisterStation)
		r.Post("/tasks", h.PostTask)
		r.Get("/tasks", h.GetTasksHandler)
		r.Post("/stations/{stationId}/claim-next", h.PostClaimNext)
		r.Post("/stations/{stationId}/check-in", h.PostCheckInStation)
		r.Post("/stations/{stationId}/check-out", h.PostCheckOutStation)
		r.Post("/tasks/{id}/renew-lease", h.PostRenewLease)
		r.Post("/tasks/{id}/complete", h.PostCompleteTask)
		r.Post("/tasks/{id}/seal-package", h.PostSealPackage)
		r.Post("/packages/{id}/slam", h.PostRunSlam)
		r.Get("/queues/{taskType}/depth", h.GetQueueDepthHandler)
		r.Get("/capacity/{capability}", h.GetInstalledCapacityHandler)
		r.Post("/tasks/expire-leases", h.PostExpireLeases)
		r.Post("/rebin/arrivals", h.PostArriveAtRebin)
	})

	return r
}

// RequestLogger returns chi middleware that logs one structured line per
// request via logger, including status, duration, response size and the
// chi request id. Responses with a 5xx status are logged at Error level;
// everything else logs at Info level.
//
// It logs through the *Context flavours of the slog API so that, when the
// logger is wrapped in observability.NewSlogHandler, each line also carries
// the trace_id/span_id of the request span otelchi started upstream.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			attrs := []any{
				"method", r.Method,
				"path", sanitizeForLog(r.URL.Path),
				"status", ww.Status(),
				"duration_ms", time.Since(start).Milliseconds(),
				"bytes", ww.BytesWritten(),
				"request_id", middleware.GetReqID(r.Context()),
			}
			if ww.Status() >= 500 {
				logger.ErrorContext(r.Context(), "http request", attrs...)
			} else {
				logger.InfoContext(r.Context(), "http request", attrs...)
			}
		})
	}
}

// sanitizeForLog strips CR/LF from an attacker-controlled value (here,
// the raw request path) before it is written to a log line. Without this,
// a crafted path segment containing an encoded newline could forge a fake
// log entry that appears to be a separate, legitimate line (CWE-117 log
// injection) once the log record reaches a downstream viewer/aggregator
// that doesn't preserve slog's JSON string escaping.
func sanitizeForLog(s string) string {
	return strings.NewReplacer("\n", "", "\r", "").Replace(s)
}

// corsMiddleware allows the warehouse-console browser SPA (and this
// service's own future MFE remote dev origin) to call this API directly
// from the browser. Static-bearer-key auth, not cookies, so credentials
// are never needed here. CORS_ALLOWED_ORIGINS overrides the local-dev
// default (comma-separated) for staging/prod deployments.
func corsMiddleware() func(http.Handler) http.Handler {
	origins := []string{"http://localhost:5173", "http://localhost:5184"}
	if v := os.Getenv("CORS_ALLOWED_ORIGINS"); v != "" {
		origins = strings.Split(v, ",")
	}
	return cors.Handler(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: false,
		MaxAge:           300,
	})
}
