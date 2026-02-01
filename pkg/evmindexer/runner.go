package evmindexer

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

//go:embed indexer_tables.sql
var indexerTablesSQL string

// IndexRunner processes indexers for a single chain
type IndexRunner struct {
	chainId    uint32
	conn       driver.Conn
	sqlDir     string
	startBlock uint64 // First block to index (from config)
	rpcURL     string // RPC URL for token metadata fetching

	// Block state (updated by OnBlock)
	latestBlockNum  uint64
	latestBlockTime time.Time

	// Watermarks (in-memory cache, backed by DB)
	watermarks map[string]*Watermark

	// Discovered indexers (loaded once at startup)
	granularMetrics     []string
	incrementalIndexers []string

	// Token metadata fetcher
	tokenMetadataFetcher *TokenMetadataFetcher
	lastMetadataFetch    time.Time
}

// NewIndexRunner creates a new indexer runner for a single chain
func NewIndexRunner(chainId uint32, conn driver.Conn, sqlDir string, startBlock uint64, rpcURL string) (*IndexRunner, error) {
	// Create tables from indexer_tables.sql (metrics and indexer_watermarks)
	// Execute each CREATE TABLE statement
	statements := splitSQL(indexerTablesSQL)
	for _, stmt := range statements {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if err := conn.Exec(context.Background(), stmt); err != nil {
			// Ignore "already exists" errors
			if !strings.Contains(err.Error(), "already exists") {
				return nil, fmt.Errorf("failed to create table from indexer_tables.sql: %w", err)
			}
		}
	}

	// Create token_metadata table
	tokenMetadataSQL := `
		CREATE TABLE IF NOT EXISTS token_metadata (
			chain_id UInt32,
			token FixedString(20),
			name String,
			symbol String,
			decimals UInt8,
			computed_at DateTime64(3, 'UTC') DEFAULT now64(3)
		) ENGINE = ReplacingMergeTree(computed_at)
		ORDER BY (chain_id, token)
	`
	if err := conn.Exec(context.Background(), tokenMetadataSQL); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return nil, fmt.Errorf("failed to create token_metadata table: %w", err)
		}
	}

	runner := &IndexRunner{
		chainId:    chainId,
		conn:       conn,
		sqlDir:     sqlDir,
		startBlock: startBlock,
		rpcURL:     rpcURL,
		watermarks: make(map[string]*Watermark),
	}

	// Initialize token metadata fetcher if RPC URL provided
	if rpcURL != "" {
		runner.tokenMetadataFetcher = NewTokenMetadataFetcher(chainId, conn, rpcURL)
		fmt.Printf("[Chain %d] Token metadata fetcher initialized with RPC: %s\n", chainId, rpcURL)
	} else {
		fmt.Printf("[Chain %d] Token metadata fetcher NOT initialized (no RPC URL)\n", chainId)
	}

	// Discover indexers
	if err := runner.discoverIndexers(); err != nil {
		return nil, fmt.Errorf("failed to discover indexers: %w", err)
	}

	// Load watermarks from DB
	if err := runner.loadWatermarks(); err != nil {
		return nil, fmt.Errorf("failed to load watermarks: %w", err)
	}

	fmt.Printf("[Chain %d] IndexRunner initialized - %d granular metrics, %d incremental indexers\n",
		chainId, len(runner.granularMetrics), len(runner.incrementalIndexers))

	return runner, nil
}

// discoverIndexers scans filesystem for SQL files
func (r *IndexRunner) discoverIndexers() error {
	var err error

	// Discover granular metrics
	r.granularMetrics, err = discoverSQLFiles(filepath.Join(r.sqlDir, "evm_metrics"))
	if err != nil {
		return err
	}

	// Discover incremental indexers
	r.incrementalIndexers, err = discoverSQLFiles(filepath.Join(r.sqlDir, "evm_incremental"))
	if err != nil {
		return err
	}

	return nil
}

// OnBlock updates the runner with latest block information
func (r *IndexRunner) OnBlock(blockNum uint64, blockTime time.Time) {
	r.latestBlockNum = blockNum
	r.latestBlockTime = blockTime
}

// Start begins the indexer loop (runs forever)
func (r *IndexRunner) Start() {
	fmt.Printf("[Chain %d] Starting indexer loop (tokenMetadataFetcher=%v)\n", r.chainId, r.tokenMetadataFetcher != nil)

	for {
		// Only process if we have block data
		if r.latestBlockNum == 0 {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Process all pending blocks for incremental indexers
		hasWork := r.processIncrementalBatch()

		// Process granular metrics (time-based)
		r.processGranularMetrics()

		// Fetch token metadata periodically (every 30 seconds)
		if r.tokenMetadataFetcher != nil && time.Since(r.lastMetadataFetch) > 30*time.Second {
			r.lastMetadataFetch = time.Now()
			count, err := r.tokenMetadataFetcher.FetchMissingMetadata(100)
			if err != nil {
				fmt.Printf("[Chain %d] Token metadata fetch error: %v\n", r.chainId, err)
			} else if count > 0 {
				fmt.Printf("[Chain %d] Fetched metadata for %d tokens\n", r.chainId, count)
			} else {
				fmt.Printf("[Chain %d] Token metadata: no missing tokens found\n", r.chainId)
			}
		}

		// Sleep only if no incremental work was done
		if !hasWork {
			time.Sleep(100 * time.Millisecond)
		}
	}
}
