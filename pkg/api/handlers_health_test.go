package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthHandler_Success(t *testing.T) {
	mock := &MockConn{
		PingFunc: func(ctx context.Context) error {
			return nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/health")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)

	resp := ParseResponse[HealthStatus](t, w)
	assert.Equal(t, "healthy", resp.Status)
	assert.Equal(t, "connected", resp.Database)
}

func TestHealthHandler_DatabaseError(t *testing.T) {
	mock := &MockConn{
		PingFunc: func(ctx context.Context) error {
			return errors.New("connection refused")
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/health")

	AssertJSONResponse(t, w, http.StatusServiceUnavailable)

	resp := ParseResponse[HealthStatus](t, w)
	assert.Equal(t, "unhealthy", resp.Status)
	assert.Equal(t, "disconnected", resp.Database)
}

func TestHealthHandler_MethodNotAllowed(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)

	// POST should not be allowed
	w := MakeRequest(t, server, "POST", "/health")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
