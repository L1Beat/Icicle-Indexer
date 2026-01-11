package api

import (
	"net/http"
)

type HealthStatus struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()

	// Check database connectivity
	err := s.conn.Ping(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, HealthStatus{
			Status:   "unhealthy",
			Database: "disconnected",
		})
		return
	}

	writeJSON(w, http.StatusOK, HealthStatus{
		Status:   "healthy",
		Database: "connected",
	})
}
