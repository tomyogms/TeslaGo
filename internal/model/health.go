package model

import "time"

type HealthResponse struct {
	Timestamp time.Time      `json:"timestamp"`
	Status    string         `json:"status"`
	Database  DatabaseStatus `json:"database"`
}

type DatabaseStatus struct {
	Status string `json:"status"`
}
