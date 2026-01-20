package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer
	pongWait = 60 * time.Second

	// Send pings to peer with this period (must be less than pongWait)
	pingPeriod = 30 * time.Second

	// Maximum message size allowed from peer
	maxMessageSize = 512

	// Poll interval for checking new blocks
	pollInterval = 500 * time.Millisecond
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // Allow all origins
}

// WSMessage represents a WebSocket message
type WSMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data,omitempty"`
}

// WSHub manages WebSocket connections grouped by chain_id
type WSHub struct {
	clients    map[uint32]map[*WSClient]bool // chain_id -> set of clients
	register   chan *WSClient
	unregister chan *WSClient
	mu         sync.RWMutex
	conn       driver.Conn
	lastBlock  map[uint32]uint64 // chain_id -> last seen block number
}

// WSClient represents a single WebSocket connection
type WSClient struct {
	hub     *WSHub
	conn    *websocket.Conn
	send    chan []byte
	chainID uint32
}

// NewWSHub creates a new WebSocket hub
func NewWSHub(conn driver.Conn) *WSHub {
	return &WSHub{
		clients:    make(map[uint32]map[*WSClient]bool),
		register:   make(chan *WSClient),
		unregister: make(chan *WSClient),
		conn:       conn,
		lastBlock:  make(map[uint32]uint64),
	}
}

// Run starts the hub's main loop
func (h *WSHub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			if h.clients[client.chainID] == nil {
				h.clients[client.chainID] = make(map[*WSClient]bool)
			}
			h.clients[client.chainID][client] = true
			log.Printf("[WS] Client connected to chain %d (total: %d)", client.chainID, len(h.clients[client.chainID]))
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if clients, ok := h.clients[client.chainID]; ok {
				if _, exists := clients[client]; exists {
					delete(clients, client)
					close(client.send)
					log.Printf("[WS] Client disconnected from chain %d (remaining: %d)", client.chainID, len(clients))
					if len(clients) == 0 {
						delete(h.clients, client.chainID)
						delete(h.lastBlock, client.chainID)
					}
				}
			}
			h.mu.Unlock()
		}
	}
}

// StartPoller starts polling for new blocks
func (h *WSHub) StartPoller() {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for range ticker.C {
		h.pollNewBlocks()
	}
}

// pollNewBlocks checks for new blocks and broadcasts them
func (h *WSHub) pollNewBlocks() {
	h.mu.RLock()
	chainIDs := make([]uint32, 0, len(h.clients))
	for chainID := range h.clients {
		chainIDs = append(chainIDs, chainID)
	}
	h.mu.RUnlock()

	if len(chainIDs) == 0 {
		return
	}

	ctx := context.Background()

	for _, chainID := range chainIDs {
		// Get latest block number (use uint32 to match ClickHouse schema)
		var latestBlock uint32
		row := h.conn.QueryRow(ctx, `
			SELECT max(block_number) FROM raw_blocks WHERE chain_id = ?
		`, chainID)
		if err := row.Scan(&latestBlock); err != nil {
			log.Printf("[WS Poller] Error querying latest block for chain %d: %v", chainID, err)
			continue
		}

		h.mu.RLock()
		lastSeen := h.lastBlock[chainID]
		h.mu.RUnlock()

		// Initialize lastBlock if this is the first poll for this chain
		if lastSeen == 0 {
			h.mu.Lock()
			h.lastBlock[chainID] = uint64(latestBlock)
			h.mu.Unlock()
			log.Printf("[WS Poller] Initialized chain %d at block %d", chainID, latestBlock)
			continue
		}

		// Check if there are new blocks
		if uint64(latestBlock) > lastSeen {
			// Fetch new blocks
			blocks := h.fetchBlockRange(ctx, chainID, lastSeen+1, uint64(latestBlock))
			log.Printf("[WS Poller] Chain %d: %d new blocks (%d -> %d)", chainID, len(blocks), lastSeen+1, latestBlock)
			for _, block := range blocks {
				h.broadcastBlock(chainID, block)
			}

			h.mu.Lock()
			h.lastBlock[chainID] = uint64(latestBlock)
			h.mu.Unlock()
		}
	}
}

// fetchBlockRange fetches blocks in a range
func (h *WSHub) fetchBlockRange(ctx context.Context, chainID uint32, from, to uint64) []Block {
	rows, err := h.conn.Query(ctx, `
		SELECT
			chain_id, block_number, hash, parent_hash, block_time,
			miner, size, gas_limit, gas_used, base_fee_per_gas, tx_count
		FROM raw_blocks
		WHERE chain_id = ? AND block_number >= ? AND block_number <= ?
		ORDER BY block_number ASC
	`, chainID, from, to)
	if err != nil {
		log.Printf("[WS] Error fetching blocks: %v", err)
		return nil
	}
	defer rows.Close()

	var blocks []Block
	for rows.Next() {
		var b Block
		var hashBytes, parentHashBytes [32]byte
		var minerAddr [20]byte

		if err := rows.Scan(
			&b.ChainID, &b.BlockNumber, &hashBytes, &parentHashBytes, &b.BlockTime,
			&minerAddr, &b.Size, &b.GasLimit, &b.GasUsed, &b.BaseFee, &b.TxCount,
		); err != nil {
			continue
		}

		b.Hash = "0x" + hex.EncodeToString(hashBytes[:])
		b.ParentHash = "0x" + hex.EncodeToString(parentHashBytes[:])
		b.Miner = "0x" + hex.EncodeToString(minerAddr[:])
		blocks = append(blocks, b)
	}

	return blocks
}

// broadcastBlock sends a block to all clients subscribed to the chain
func (h *WSHub) broadcastBlock(chainID uint32, block Block) {
	msg := WSMessage{
		Type: "new_block",
		Data: block,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	h.mu.RLock()
	clients := h.clients[chainID]
	h.mu.RUnlock()

	for client := range clients {
		select {
		case client.send <- data:
		default:
			// Client's buffer is full, skip
		}
	}
}

// getRecentBlocks fetches the most recent blocks for a chain
func (h *WSHub) getRecentBlocks(chainID uint32, limit int) []Block {
	ctx := context.Background()

	rows, err := h.conn.Query(ctx, `
		SELECT
			chain_id, block_number, hash, parent_hash, block_time,
			miner, size, gas_limit, gas_used, base_fee_per_gas, tx_count
		FROM raw_blocks
		WHERE chain_id = ?
		ORDER BY block_number DESC
		LIMIT ?
	`, chainID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var blocks []Block
	for rows.Next() {
		var b Block
		var hashBytes, parentHashBytes [32]byte
		var minerAddr [20]byte

		if err := rows.Scan(
			&b.ChainID, &b.BlockNumber, &hashBytes, &parentHashBytes, &b.BlockTime,
			&minerAddr, &b.Size, &b.GasLimit, &b.GasUsed, &b.BaseFee, &b.TxCount,
		); err != nil {
			continue
		}

		b.Hash = "0x" + hex.EncodeToString(hashBytes[:])
		b.ParentHash = "0x" + hex.EncodeToString(parentHashBytes[:])
		b.Miner = "0x" + hex.EncodeToString(minerAddr[:])
		blocks = append(blocks, b)
	}

	// Reverse to get oldest first (chronological order)
	for i, j := 0, len(blocks)-1; i < j; i, j = i+1, j-1 {
		blocks[i], blocks[j] = blocks[j], blocks[i]
	}

	return blocks
}

// readPump pumps messages from the websocket connection to the hub
func (c *WSClient) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[WS] Read error: %v", err)
			}
			break
		}
	}
}

// writePump pumps messages from the hub to the websocket connection
func (c *WSClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))

			// Send ping message
			pingMsg, _ := json.Marshal(WSMessage{Type: "ping"})
			if err := c.conn.WriteMessage(websocket.TextMessage, pingMsg); err != nil {
				return
			}
		}
	}
}

// handleWSBlocks handles WebSocket connections for block streaming
func (s *Server) handleWSBlocks(w http.ResponseWriter, r *http.Request) {
	chainID, err := getChainIDFromPath(r)
	if err != nil {
		http.Error(w, "Invalid chain_id", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS] Upgrade error: %v", err)
		return
	}

	client := &WSClient{
		hub:     s.wsHub,
		conn:    conn,
		send:    make(chan []byte, 256),
		chainID: chainID,
	}

	// Send initial blocks
	blocks := s.wsHub.getRecentBlocks(chainID, 10)
	initialMsg := WSMessage{
		Type: "initial",
		Data: blocks,
	}
	if err := conn.WriteJSON(initialMsg); err != nil {
		log.Printf("[WS] Error sending initial blocks: %v", err)
		conn.Close()
		return
	}

	// Register client
	s.wsHub.register <- client

	// Start pumps
	go client.writePump()
	client.readPump() // Blocks until disconnect
}
