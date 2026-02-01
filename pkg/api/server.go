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
	httpSwagger "github.com/swaggo/http-swagger/v2"

	_ "icicle/docs" // swagger docs
)

// @title Icicle API
// @version 1.0
// @description Avalanche P-Chain and EVM Indexer API
// @description
// @description ## Overview
// @description This API provides access to indexed Avalanche blockchain data including:
// @description - **Data API** (`/api/v1/data/*`): Blocks, transactions, subnets, validators, P-chain data
// @description - **Metrics API** (`/api/v1/metrics/*`): Fee statistics, chain metrics, time series data
// @description
// @description ## Rate Limiting
// @description All endpoints are rate limited to 60 requests/minute per IP with a burst of 10.

// @host localhost:8080
// @BasePath /
// @schemes http https

// @tag.name Health
// @tag.description Health check endpoints

// @tag.name Data - EVM
// @tag.description EVM blockchain data (blocks, transactions)

// @tag.name Data - P-Chain
// @tag.description P-Chain transaction data

// @tag.name Data - Subnets
// @tag.description Subnet and L1 information

// @tag.name Data - Validators
// @tag.description L1 validator information

// @tag.name Metrics - Fees
// @tag.description L1 validation fee statistics

// @tag.name Metrics - Chain
// @tag.description EVM chain statistics and time series

// @tag.name Metrics - Indexer
// @tag.description Indexer sync status

// Config holds API server configuration
type Config struct {
	RateLimit RateLimitConfig
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		RateLimit: DefaultRateLimitConfig(),
	}
}

type Server struct {
	conn        driver.Conn
	router      *http.ServeMux
	rateLimiter *RateLimiter
	handler     http.Handler
	wsHub       *WSHub
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

func NewServer(conn driver.Conn, cfg Config) *Server {
	s := &Server{
		conn:        conn,
		router:      http.NewServeMux(),
		rateLimiter: NewRateLimiter(cfg.RateLimit),
		wsHub:       NewWSHub(conn),
	}
	s.registerRoutes()

	// Start WebSocket hub
	go s.wsHub.Run()
	go s.wsHub.StartPoller()

	// Chain middlewares: CORS -> Logging -> RateLimit -> Router
	s.handler = Chain(
		s.router,
		CORSMiddleware("*"),
		LoggingMiddleware,
		s.rateLimiter.Middleware,
	)

	return s
}

func (s *Server) registerRoutes() {
	// System endpoints (no versioning)
	s.router.HandleFunc("GET /health", s.handleHealth)

	// Swagger documentation
	s.router.HandleFunc("GET /api/docs/", httpSwagger.WrapHandler)

	// ==========================================
	// Data API - /api/v1/data/*
	// ==========================================

	// EVM Blocks
	s.router.HandleFunc("GET /api/v1/data/evm/{chainId}/blocks", s.handleListBlocks)
	s.router.HandleFunc("GET /api/v1/data/evm/{chainId}/blocks/{number}", s.handleGetBlock)

	// EVM Transactions
	s.router.HandleFunc("GET /api/v1/data/evm/{chainId}/txs", s.handleListTxs)
	s.router.HandleFunc("GET /api/v1/data/evm/{chainId}/txs/{hash}", s.handleGetTx)
	s.router.HandleFunc("GET /api/v1/data/evm/{chainId}/address/{address}/txs", s.handleAddressTxs)

	// EVM Address Balances
	s.router.HandleFunc("GET /api/v1/data/evm/{chainId}/address/{address}/balances", s.handleAddressBalances)
	s.router.HandleFunc("GET /api/v1/data/evm/{chainId}/address/{address}/native", s.handleAddressNativeBalance)

	// P-Chain Transactions
	s.router.HandleFunc("GET /api/v1/data/pchain/txs", s.handleListPChainTxs)
	s.router.HandleFunc("GET /api/v1/data/pchain/txs/{txId}", s.handleGetPChainTx)
	s.router.HandleFunc("GET /api/v1/data/pchain/tx-types", s.handlePChainTxTypes)

	// Subnets
	s.router.HandleFunc("GET /api/v1/data/subnets", s.handleListSubnets)
	s.router.HandleFunc("GET /api/v1/data/subnets/{subnetId}", s.handleGetSubnet)

	// L1s (subset of subnets)
	s.router.HandleFunc("GET /api/v1/data/l1s", s.handleListL1s)

	// Chains (blockchains within subnets)
	s.router.HandleFunc("GET /api/v1/data/chains", s.handleListChains)

	// Validators
	s.router.HandleFunc("GET /api/v1/data/validators", s.handleListValidators)
	s.router.HandleFunc("GET /api/v1/data/validators/{id}", s.handleGetValidator)
	s.router.HandleFunc("GET /api/v1/data/validators/{id}/deposits", s.handleValidatorDeposits)

	// ==========================================
	// Metrics API - /api/v1/metrics/*
	// ==========================================

	// L1 Fee Metrics
	s.router.HandleFunc("GET /api/v1/metrics/fees", s.handleFeeMetrics)

	// EVM Chain Stats & Time Series
	s.router.HandleFunc("GET /api/v1/metrics/evm/{chainId}/stats", s.handleChainMetrics)
	s.router.HandleFunc("GET /api/v1/metrics/evm/{chainId}/timeseries", s.handleListMetrics)
	s.router.HandleFunc("GET /api/v1/metrics/evm/{chainId}/timeseries/{metric}", s.handleGetMetric)

	// Indexer Status
	s.router.HandleFunc("GET /api/v1/metrics/indexer/status", s.handleIndexerStatus)

	// ==========================================
	// WebSocket - /ws/*
	// ==========================================
	s.router.HandleFunc("GET /ws/blocks/{chainId}", s.handleWSBlocks)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) Start(port int) error {
	addr := fmt.Sprintf(":%d", port)
	log.Printf("Starting API server on %s", addr)
	log.Printf("  Data API:    /api/v1/data/*")
	log.Printf("  Metrics API: /api/v1/metrics/*")
	log.Printf("  WebSocket:   /ws/blocks/{chainId}")
	return http.ListenAndServe(addr, s)
}

// Stop cleans up server resources
func (s *Server) Stop() {
	if s.rateLimiter != nil {
		s.rateLimiter.Stop()
	}
}

// Helper functions

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
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
