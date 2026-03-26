package handler

import (
	"encoding/json"
	"net/http"

	"github.com/tomyogms/TeslaGo/internal/service"
)

type HealthHandler struct {
	service service.HealthService
}

func NewHealthHandler(s service.HealthService) *HealthHandler {
	return &HealthHandler{service: s}
}

func (h *HealthHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	health, err := h.service.CheckHealth(r.Context())

	w.Header().Set("Content-Type", "application/json")

	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(health)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(health)
}
