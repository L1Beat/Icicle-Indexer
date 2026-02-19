package api

import (
	"net/http"
)

// HealthStatus represents the health check response
type HealthStatus struct {
	Status   string `json:"status" example:"healthy"`
	Database string `json:"database" example:"connected"`
}

// handleHealth checks API and database health
// @Summary Health check
// @Description Check API server and database connectivity
// @Tags Health
// @Produce json
// @Success 200 {object} HealthStatus "Healthy"
// @Failure 503 {object} HealthStatus "Unhealthy"
// @Router /health [get]
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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
