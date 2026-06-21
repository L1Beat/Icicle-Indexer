package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"math/big"
	"net/http"
	"time"
)

// WSLendingAlert is the payload pushed when a position crosses a risk threshold.
type WSLendingAlert struct {
	Account        string    `json:"account"`
	Protocol       string    `json:"protocol"`
	Kind           string    `json:"kind"`
	HealthFactor   string    `json:"health_factor"`
	CollateralBase string    `json:"collateral_base"`
	DebtBase       string    `json:"debt_base"`
	BlockNumber    uint32    `json:"block_number"`
	CreatedAt      time.Time `json:"created_at"`
}

// handleWSLending streams lending liquidation-risk alerts for a chain, reusing
// the block hub's connection management and pumps.
func (s *Server) handleWSLending(w http.ResponseWriter, r *http.Request) {
	chainID, err := getChainIDFromPath(r)
	if err != nil {
		http.Error(w, "Invalid chain_id", http.StatusBadRequest)
		return
	}

	ip := s.wsHub.clientIP(r)
	if !s.wsHub.reserveConnection(chainID, ip) {
		writeAPIError(w, http.StatusTooManyRequests, ErrRateLimited, "WebSocket connection limit exceeded")
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.wsHub.releaseReservedConnection(ip)
		slog.Error("WS upgrade error", "error", err)
		return
	}

	client := &WSClient{
		hub:      s.wsHub,
		conn:     conn,
		send:     make(chan []byte, 256),
		chainID:  chainID,
		ip:       ip,
		reserved: true,
		feed:     "lending",
	}

	initial := WSMessage{Type: "initial", Data: s.wsHub.getRecentAlerts(chainID, 20)}
	if err := conn.WriteJSON(initial); err != nil {
		s.wsHub.releaseReservedConnection(ip)
		conn.Close()
		return
	}

	s.wsHub.register <- client
	go client.writePump()
	client.readPump()
}

// pollNewAlerts broadcasts lending alerts created since the last poll to lending
// subscribers, mirroring the block poller's catch-up model.
func (h *WSHub) pollNewAlerts() {
	h.mu.RLock()
	var chainIDs []uint32
	for chainID, clients := range h.clients {
		for c := range clients {
			if c.feed == "lending" {
				chainIDs = append(chainIDs, chainID)
				break
			}
		}
	}
	h.mu.RUnlock()
	if len(chainIDs) == 0 {
		return
	}

	ctx := context.Background()
	for _, chainID := range chainIDs {
		var maxTime time.Time
		row := h.conn.QueryRow(ctx, `SELECT max(created_at) FROM lending_alerts WHERE chain_id = ?`, chainID)
		if err := row.Scan(&maxTime); err != nil || maxTime.IsZero() {
			continue
		}

		h.mu.RLock()
		last := h.lastAlert[chainID]
		h.mu.RUnlock()

		if last.IsZero() {
			h.mu.Lock()
			h.lastAlert[chainID] = maxTime
			h.mu.Unlock()
			continue
		}
		if !maxTime.After(last) {
			continue
		}

		for _, a := range h.fetchAlertsSince(ctx, chainID, last) {
			h.broadcastAlert(chainID, a)
		}
		h.mu.Lock()
		h.lastAlert[chainID] = maxTime
		h.mu.Unlock()
	}
}

func (h *WSHub) fetchAlertsSince(ctx context.Context, chainID uint32, since time.Time) []WSLendingAlert {
	rows, err := h.conn.Query(ctx, `
		SELECT account, protocol, kind, health_factor, collateral_base, debt_base, block_number, created_at
		FROM lending_alerts
		WHERE chain_id = ? AND created_at > ?
		ORDER BY created_at ASC
	`, chainID, since)
	if err != nil {
		slog.Error("WS error fetching lending alerts", "error", err)
		return nil
	}
	defer rows.Close()
	return scanAlerts(rows)
}

func (h *WSHub) getRecentAlerts(chainID uint32, limit int) []WSLendingAlert {
	rows, err := h.conn.Query(context.Background(), `
		SELECT account, protocol, kind, health_factor, collateral_base, debt_base, block_number, created_at
		FROM lending_alerts
		WHERE chain_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, chainID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	alerts := scanAlerts(rows)
	// Return oldest first for a stable initial render.
	for i, j := 0, len(alerts)-1; i < j; i, j = i+1, j-1 {
		alerts[i], alerts[j] = alerts[j], alerts[i]
	}
	return alerts
}

func scanAlerts(rows interface {
	Next() bool
	Scan(...interface{}) error
}) []WSLendingAlert {
	var out []WSLendingAlert
	for rows.Next() {
		var acc [20]byte
		var a WSLendingAlert
		var hf, coll, debt *big.Int
		if err := rows.Scan(&acc, &a.Protocol, &a.Kind, &hf, &coll, &debt, &a.BlockNumber, &a.CreatedAt); err != nil {
			return out
		}
		a.Account = "0x" + hex.EncodeToString(acc[:])
		a.HealthFactor = bigStr(hf)
		a.CollateralBase = bigStr(coll)
		a.DebtBase = bigStr(debt)
		out = append(out, a)
	}
	return out
}

func (h *WSHub) broadcastAlert(chainID uint32, alert WSLendingAlert) {
	msg := WSMessage{Type: "lending_alert", Data: alert}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.mu.RLock()
	clients := h.clients[chainID]
	h.mu.RUnlock()
	for client := range clients {
		if client.feed != "lending" {
			continue
		}
		select {
		case client.send <- data:
		default:
		}
	}
}
