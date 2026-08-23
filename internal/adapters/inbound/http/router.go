package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter builds the chi router for every Fulfillment Execution endpoint.
// A nil logger falls back to slog.Default() rather than panicking.
func NewRouter(h *Handlers, logger *slog.Logger) *chi.Mux {
	if logger == nil {
		logger = slog.Default()
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(RequestLogger(logger))
	r.Use(middleware.Recoverer)

	r.Get("/healthz", h.GetHealthz)
	r.Post("/stations", h.PostRegisterStation)
	r.Post("/tasks", h.PostTask)
	r.Post("/stations/{stationId}/claim-next", h.PostClaimNext)
	r.Post("/tasks/{id}/renew-lease", h.PostRenewLease)
	r.Post("/tasks/{id}/complete", h.PostCompleteTask)
	r.Post("/tasks/{id}/seal-package", h.PostSealPackage)
	r.Post("/packages/{id}/slam", h.PostRunSlam)
	r.Get("/queues/{taskType}/depth", h.GetQueueDepthHandler)
	r.Post("/tasks/expire-leases", h.PostExpireLeases)

	return r
}

// RequestLogger returns chi middleware that logs one structured line per
// request via logger, including status, duration, response size and the
// chi request id. Responses with a 5xx status are logged at Error level;
// everything else logs at Info level.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"duration_ms", time.Since(start).Milliseconds(),
				"bytes", ww.BytesWritten(),
				"request_id", middleware.GetReqID(r.Context()),
			}
			if ww.Status() >= 500 {
				logger.Error("http request", attrs...)
			} else {
				logger.Info("http request", attrs...)
			}
		})
	}
}
