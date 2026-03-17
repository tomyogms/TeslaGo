package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/tomyogms/TeslaGo/internal/service"
)

type HealthHandler struct {
	service service.HealthService
}

func NewHealthHandler(s service.HealthService) *HealthHandler {
	return &HealthHandler{service: s}
}

func (h *HealthHandler) HealthCheck(c *gin.Context) {
	health, err := h.service.CheckHealth(c.Request.Context())

	if err != nil {
		c.JSON(http.StatusServiceUnavailable, health)
		return
	}

	c.JSON(http.StatusOK, health)
}
