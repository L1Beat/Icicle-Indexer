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

// fakeBurnRow feeds preset values into scanBlockWithBurn's Scan dest pointers.
type fakeBurnRow struct {
	txCount    uint32
	total      uint64
	base       uint64
	seenTxRows uint64
}

func (f fakeBurnRow) Scan(dest ...interface{}) error {
	*dest[10].(*uint32) = f.txCount    // tx_count
	*dest[11].(*uint64) = f.total      // total_navax
	*dest[12].(*uint64) = f.base       // base_navax
	*dest[13].(*uint64) = f.seenTxRows // seen_tx_rows
	return nil
}

func TestScanBlockWithBurn(t *testing.T) {
	t.Run("ready block populates burn with tip split", func(t *testing.T) {
		b, ready, err := scanBlockWithBurn(fakeBurnRow{txCount: 2, total: 100, base: 78, seenTxRows: 2})
		assert.NoError(t, err)
		assert.True(t, ready)
		if assert.NotNil(t, b.Burned) {
			assert.Equal(t, "100", b.Burned.Total)
			assert.Equal(t, "78", b.Burned.BaseFee)
			assert.Equal(t, "22", b.Burned.PriorityFee) // total - base
		}
	})

	t.Run("incomplete txs -> not ready, no burn", func(t *testing.T) {
		b, ready, err := scanBlockWithBurn(fakeBurnRow{txCount: 2, total: 100, base: 78, seenTxRows: 1})
		assert.NoError(t, err)
		assert.False(t, ready)
		assert.Nil(t, b.Burned)
	})

	t.Run("empty block is ready with zero burn", func(t *testing.T) {
		b, ready, err := scanBlockWithBurn(fakeBurnRow{txCount: 0, total: 0, base: 0, seenTxRows: 0})
		assert.NoError(t, err)
		assert.True(t, ready)
		if assert.NotNil(t, b.Burned) {
			assert.Equal(t, "0", b.Burned.Total)
			assert.Equal(t, "0", b.Burned.PriorityFee)
		}
	})

	t.Run("base >= total clamps priority to zero", func(t *testing.T) {
		b, ready, err := scanBlockWithBurn(fakeBurnRow{txCount: 1, total: 50, base: 60, seenTxRows: 1})
		assert.NoError(t, err)
		assert.True(t, ready)
		if assert.NotNil(t, b.Burned) {
			assert.Equal(t, "0", b.Burned.PriorityFee)
		}
	})
}
