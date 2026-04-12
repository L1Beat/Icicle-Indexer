package api

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWSHubAllowConnection_EnforcesTotalLimit(t *testing.T) {
	hub := NewWSHub(&MockConn{}, WebSocketConfig{MaxConnections: 1}, nil)
	hub.total = 1

	assert.False(t, hub.allowConnection(43114, "203.0.113.10"))
}

func TestWSHubAllowConnection_EnforcesIPLimit(t *testing.T) {
	hub := NewWSHub(&MockConn{}, WebSocketConfig{MaxConnectionsPerIP: 1}, nil)
	hub.ipCounts["203.0.113.10"] = 1

	assert.False(t, hub.allowConnection(43114, "203.0.113.10"))
	assert.True(t, hub.allowConnection(43114, "203.0.113.11"))
}

func TestWSHubAllowConnection_EnforcesChainLimit(t *testing.T) {
	hub := NewWSHub(&MockConn{}, WebSocketConfig{MaxConnectionsPerChain: 1}, nil)
	hub.clients[43114] = map[*WSClient]bool{&WSClient{}: true}

	assert.False(t, hub.allowConnection(43114, "203.0.113.10"))
	assert.True(t, hub.allowConnection(43113, "203.0.113.10"))
}

func TestWSHubDefaultClientIPUsesRemoteAddr(t *testing.T) {
	hub := NewWSHub(&MockConn{}, WebSocketConfig{}, nil)
	req := httptest.NewRequest("GET", "/ws/blocks/43114", nil)
	req.RemoteAddr = "203.0.113.10:12345"

	assert.Equal(t, "203.0.113.10", hub.clientIP(req))
}
