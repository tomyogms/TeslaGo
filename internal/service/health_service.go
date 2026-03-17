package service

import (
	"context"
	"time"

	"github.com/tomyogms/TeslaGo/internal/model"
	"github.com/tomyogms/TeslaGo/internal/repository"
)

type HealthService interface {
	CheckHealth(ctx context.Context) (model.HealthResponse, error)
}

type healthService struct {
	repo repository.HealthRepository
}

func NewHealthService(repo repository.HealthRepository) HealthService {
	return &healthService{repo: repo}
}

func (s *healthService) CheckHealth(ctx context.Context) (model.HealthResponse, error) {
	err := s.repo.Ping(ctx)

	dbStatus := "up"
	appStatus := "healthy"

	if err != nil {
		dbStatus = "down"
		appStatus = "unhealthy"
	}

	return model.HealthResponse{
		Timestamp: time.Now().UTC(),
		Status:    appStatus,
		Database: model.DatabaseStatus{
			Status: dbStatus,
		},
	}, err
}
