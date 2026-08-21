package http

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter builds the chi router for every Fulfillment Execution endpoint.
func NewRouter(h *Handlers) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Get("/healthz", h.GetHealthz)
	r.Post("/tasks", h.PostTask)
	r.Post("/stations/{stationId}/claim-next", h.PostClaimNext)
	r.Post("/tasks/{id}/renew-lease", h.PostRenewLease)
	r.Post("/tasks/{id}/complete", h.PostCompleteTask)
	r.Post("/tasks/{id}/seal-package", h.PostSealPackage)
	r.Post("/packages/{id}/slam", h.PostRunSlam)
	r.Get("/queues/{taskType}/depth", h.GetQueueDepthHandler)
	r.Post("/admin/expire-leases", h.PostExpireLeases)

	return r
}
