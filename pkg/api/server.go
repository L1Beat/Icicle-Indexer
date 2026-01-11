package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

type Server struct {
	conn   driver.Conn
	router *http.ServeMux
}

type Response struct {
	Data interface{} `json:"data,omitempty"`
	Meta *Meta       `json:"meta,omitempty"`
}

type Meta struct {
	Total  int64 `json:"total,omitempty"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func NewServer(conn driver.Conn) *Server {
	s := &Server{
		conn:   conn,
		router: http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	// Health & Status
	s.router.HandleFunc("GET /health", s.handleHealth)
	s.router.HandleFunc("GET /indexer/status", s.handleIndexerStatus)

	// EVM Blocks - /evm/{chainId}/...
	s.router.HandleFunc("GET /evm/{chainId}/blocks", s.handleListBlocks)
	s.router.HandleFunc("GET /evm/{chainId}/blocks/{number}", s.handleGetBlock)

	// EVM Transactions
	s.router.HandleFunc("GET /evm/{chainId}/txs", s.handleListTxs)
	s.router.HandleFunc("GET /evm/{chainId}/txs/{hash}", s.handleGetTx)
	s.router.HandleFunc("GET /evm/{chainId}/address/{address}/txs", s.handleAddressTxs)

	// EVM Chain Metrics
	s.router.HandleFunc("GET /evm/{chainId}/stats", s.handleChainMetrics)
	s.router.HandleFunc("GET /evm/{chainId}/metrics", s.handleListMetrics)
	s.router.HandleFunc("GET /evm/{chainId}/metrics/{metric}", s.handleGetMetric)

	// P-Chain Transactions
	s.router.HandleFunc("GET /pchain/txs", s.handleListPChainTxs)
	s.router.HandleFunc("GET /pchain/txs/{txId}", s.handleGetPChainTx)
	s.router.HandleFunc("GET /pchain/tx-types", s.handlePChainTxTypes)

	// Subnets
	s.router.HandleFunc("GET /subnets", s.handleListSubnets)
	s.router.HandleFunc("GET /subnets/{subnetId}", s.handleGetSubnet)

	// L1s (subset of subnets)
	s.router.HandleFunc("GET /l1s", s.handleListL1s)

	// Chains (blockchains within subnets)
	s.router.HandleFunc("GET /chains", s.handleListChains)

	// Validators
	s.router.HandleFunc("GET /validators", s.handleListValidators)
	s.router.HandleFunc("GET /validators/{id}", s.handleGetValidator)
	s.router.HandleFunc("GET /validators/{id}/deposits", s.handleValidatorDeposits)

	// Metrics
	s.router.HandleFunc("GET /metrics/fees", s.handleFeeMetrics)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS middleware
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.router.ServeHTTP(w, r)
}

func (s *Server) Start(port int) error {
	addr := fmt.Sprintf(":%d", port)
	log.Printf("Starting API server on %s", addr)
	return http.ListenAndServe(addr, s)
}

// Helper functions

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, ErrorResponse{Error: message})
}

func getPagination(r *http.Request) (limit, offset int) {
	limit = 20
	offset = 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	return
}

func getChainIDFromPath(r *http.Request) (uint32, error) {
	chainIDStr := r.PathValue("chainId")
	parsed, err := strconv.ParseUint(chainIDStr, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(parsed), nil
}

func (s *Server) queryContext() context.Context {
	return context.Background()
}

func normalizeHash(hash string) string {
	hash = strings.ToLower(hash)
	if !strings.HasPrefix(hash, "0x") {
		hash = "0x" + hash
	}
	return hash
}

// parseFlexibleTime parses time from various formats: 2025-01-01, 2025-01-01T00:00:00Z, or unix timestamp
func parseFlexibleTime(s string) time.Time {
	// Try date only: 2025-01-01
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	// Try RFC3339: 2025-01-01T00:00:00Z
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	// Try unix timestamp
	if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(ts, 0).UTC()
	}
	return time.Time{}
}
