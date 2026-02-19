package evmsyncer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"icicle/pkg/cache"
	"icicle/pkg/chwrapper"
	"icicle/pkg/evmindexer"
	"icicle/pkg/evmrpc"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"golang.org/x/sync/errgroup"
)

const (
	// DefaultBufferSize is the maximum number of batches that can be buffered in the channel
	DefaultBufferSize = 200_000
	// DefaultFlushInterval is how often to flush blocks to ClickHouse
	DefaultFlushInterval = 1 * time.Second
	// DefaultMaxRetries is the default max retries for RPC requests
	DefaultMaxRetries = 20
	// DefaultRetryDelay is the default initial retry delay
	DefaultRetryDelay = 100 * time.Millisecond
)

// Config holds configuration for ChainSyncer
type Config struct {
	ChainID        uint32
	RpcURL         string
	StartBlock     int64        // Starting block number when no watermark exists, default 1
	MaxConcurrency int          // Maximum concurrent RPC and debug requests, default 20
	FetchBatchSize int          // Blocks per fetch, default 500
	RpcBatchSize   int          // RPC calls per HTTP request, default 100
	DebugBatchSize int          // Debug/trace calls per HTTP request, default 15
	BufferSize     int          // Channel buffer size, default 200000
	FlushInterval  time.Duration // Flush interval, default 1s
	MaxRetries     int          // Max retries for RPC requests, default 20
	RetryDelay     time.Duration // Initial retry delay, default 100ms
	CHConn         driver.Conn  // ClickHouse connection
	Cache          *cache.Cache // Cache for RPC calls
	Name           string       // Chain name for display and tracking
	Fast           bool         // Fast mode - skip all indexers
}

// ChainSyncer manages blockchain sync for a single chain
type ChainSyncer struct {
	chainId        uint32
	chainName      string
	fetcher        *evmrpc.Fetcher
	conn           driver.Conn
	blockChan      chan []*evmrpc.NormalizedBlock // Bounded channel for backpressure
	watermark      uint32                         // Current sync position
	startBlock     int64                          // Starting block when no watermark
	fetchBatchSize int
	flushInterval  time.Duration

	// Max block numbers in each table (queried once at startup)
	maxBlockBlocks       uint32
	maxBlockTransactions uint32
	maxBlockTraces       uint32
	maxBlockLogs         uint32

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Progress tracking
	mu            sync.Mutex
	blocksFetched int64
	blocksWritten int64
	lastPrintTime time.Time
	startTime     time.Time

	// Indexer runner (one per chain)
	indexerRunner *evmindexer.IndexRunner
	fast          bool // Fast mode - skip all indexers
}

// NewChainSyncer creates a new chain syncer
func NewChainSyncer(cfg Config) (*ChainSyncer, error) {
	if cfg.FetchBatchSize == 0 {
		cfg.FetchBatchSize = 500
	}
	if cfg.MaxConcurrency == 0 {
		cfg.MaxConcurrency = 20
	}
	if cfg.StartBlock == 0 {
		cfg.StartBlock = 1
	}
	if cfg.RpcBatchSize == 0 {
		cfg.RpcBatchSize = 100 // Default: batch 100 RPC calls per HTTP request
	}
	if cfg.DebugBatchSize == 0 {
		cfg.DebugBatchSize = 15 // Default: batch 15 trace calls per HTTP request
	}
	if cfg.BufferSize == 0 {
		cfg.BufferSize = DefaultBufferSize
	}
	if cfg.FlushInterval == 0 {
		cfg.FlushInterval = DefaultFlushInterval
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = DefaultMaxRetries
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = DefaultRetryDelay
	}

	// Create fetcher
	fetcher := evmrpc.NewFetcher(evmrpc.FetcherOptions{
		RpcURL:         cfg.RpcURL,
		MaxConcurrency: cfg.MaxConcurrency,
		MaxRetries:     cfg.MaxRetries,
		RetryDelay:     cfg.RetryDelay,
		BatchSize:      cfg.RpcBatchSize,
		DebugBatchSize: cfg.DebugBatchSize,
		Cache:          cfg.Cache,
	})

	ctx, cancel := context.WithCancel(context.Background())

	cs := &ChainSyncer{
		chainId:        cfg.ChainID,
		chainName:      cfg.Name,
		fetcher:        fetcher,
		conn:           cfg.CHConn,
		blockChan:      make(chan []*evmrpc.NormalizedBlock, cfg.BufferSize),
		startBlock:     cfg.StartBlock,
		fetchBatchSize: cfg.FetchBatchSize,
		flushInterval:  cfg.FlushInterval,
		ctx:            ctx,
		cancel:         cancel,
		lastPrintTime:  time.Now(),
		startTime:      time.Now(),
		fast:           cfg.Fast,
	}

	// Initialize indexer runner - one per chain (skip in fast mode)
	if !cfg.Fast {
		indexerRunner, err := evmindexer.NewIndexRunner(cfg.ChainID, cfg.CHConn, "sql", uint64(cfg.StartBlock), cfg.RpcURL)
		if err != nil {
			return nil, fmt.Errorf("failed to create indexer runner: %w", err)
		}
		cs.indexerRunner = indexerRunner
		slog.Info("Indexer runner initialized", "chain_id", cfg.ChainID)
	} else {
		slog.Info("Fast mode - indexers disabled", "chain_id", cfg.ChainID)
	}

	return cs, nil
}

// Start begins syncing
func (cs *ChainSyncer) Start() error {
	slog.Info("Starting syncer", "chain_id", cs.chainId)

	// Get starting position
	startBlock, err := cs.getStartingBlock()
	if err != nil {
		return fmt.Errorf("failed to determine starting block: %w", err)
	}

	// Query max block for each table once at startup
	// This is critical for preventing duplicates - we only insert blocks > maxBlock
	cs.maxBlockBlocks, err = chwrapper.GetLatestBlockForChain(cs.conn, "raw_blocks", cs.chainId)
	if err != nil {
		return fmt.Errorf("failed to get max block from blocks table: %w", err)
	}

	cs.maxBlockTransactions, err = chwrapper.GetLatestBlockForChain(cs.conn, "raw_txs", cs.chainId)
	if err != nil {
		return fmt.Errorf("failed to get max block from transactions table: %w", err)
	}

	cs.maxBlockTraces, err = chwrapper.GetLatestBlockForChain(cs.conn, "raw_traces", cs.chainId)
	if err != nil {
		return fmt.Errorf("failed to get max block from traces table: %w", err)
	}

	cs.maxBlockLogs, err = chwrapper.GetLatestBlockForChain(cs.conn, "raw_logs", cs.chainId)
	if err != nil {
		return fmt.Errorf("failed to get max block from logs table: %w", err)
	}

	slog.Info("Max blocks in tables", "chain_id", cs.chainId, "blocks", cs.maxBlockBlocks, "txs", cs.maxBlockTransactions, "traces", cs.maxBlockTraces, "logs", cs.maxBlockLogs)
	slog.Info("Starting from block", "chain_id", cs.chainId, "start_block", startBlock, "watermark", cs.watermark)

	// Get latest block from RPC
	latestBlock, err := cs.fetcher.GetLatestBlock()
	if err != nil {
		return fmt.Errorf("failed to get latest block: %w", err)
	}

	slog.Info("Latest block on chain", "chain_id", cs.chainId, "latest_block", latestBlock)

	// Initialize chain status in database
	if err := chwrapper.UpsertChainStatus(cs.conn, cs.chainId, cs.chainName, uint64(latestBlock)); err != nil {
		return fmt.Errorf("failed to upsert chain status: %w", err)
	}

	// Start producer (fetcher) goroutine
	cs.wg.Add(1)
	go cs.fetcherLoop(startBlock, latestBlock)

	// Start consumer (writer) goroutine
	cs.wg.Add(1)
	go cs.writerLoop()

	// Start progress printer
	cs.wg.Add(1)
	go cs.printProgress()

	// Start indexer loop (skip in fast mode)
	if !cs.fast {
		// Initialize indexer with latest known block if we have one
		if cs.maxBlockBlocks > 0 {
			// Query block time for the latest block
			blockTime, err := cs.getBlockTime(cs.maxBlockBlocks)
			if err != nil {
				slog.Warn("Failed to get block time", "chain_id", cs.chainId, "block", cs.maxBlockBlocks, "error", err)
				// Use current time as fallback
				blockTime = time.Now().UTC()
			}
			cs.indexerRunner.OnBlock(uint64(cs.maxBlockBlocks), blockTime)
			slog.Info("Initialized indexer with block", "chain_id", cs.chainId, "block", cs.maxBlockBlocks)
		}

		cs.wg.Add(1)
		go func() {
			defer cs.wg.Done()
			cs.indexerRunner.Start()
		}()
	}

	return nil
}

// Stop gracefully shuts down the syncer
func (cs *ChainSyncer) Stop() {
	slog.Info("Stopping syncer", "chain_id", cs.chainId)
	cs.cancel()
	close(cs.blockChan)
	cs.wg.Wait()
	slog.Info("Syncer stopped", "chain_id", cs.chainId)
}

// Wait blocks until syncer completes
func (cs *ChainSyncer) Wait() {
	cs.wg.Wait()
}

// getBlockTime queries the block time for a specific block from the database
func (cs *ChainSyncer) getBlockTime(blockNum uint32) (time.Time, error) {
	ctx := context.Background()
	query := "SELECT block_time FROM raw_blocks WHERE chain_id = ? AND block_number = ? LIMIT 1"

	row := cs.conn.QueryRow(ctx, query, cs.chainId, blockNum)
	var blockTime time.Time
	if err := row.Scan(&blockTime); err != nil {
		return time.Time{}, fmt.Errorf("failed to query block time: %w", err)
	}

	return blockTime, nil
}

// getStartingBlock determines where to start syncing from
func (cs *ChainSyncer) getStartingBlock() (int64, error) {
	// Get watermark
	watermark, err := chwrapper.GetWatermark(cs.conn, cs.chainId)
	if err != nil {
		return 0, fmt.Errorf("failed to get watermark: %w", err)
	}
	cs.watermark = watermark

	// If no watermark, start from configured start block
	if watermark == 0 {
		return cs.startBlock, nil
	}

	// Start from watermark+1
	return int64(watermark + 1), nil
}

// fetcherLoop is the producer goroutine that fetches blocks
func (cs *ChainSyncer) fetcherLoop(startBlock, latestBlock int64) {
	defer cs.wg.Done()

	currentBlock := startBlock

	for {
		select {
		case <-cs.ctx.Done():
			return
		default:
			// Check if we're caught up
			if currentBlock > latestBlock {
				// Poll for new blocks (500ms for near real-time updates)
				time.Sleep(500 * time.Millisecond)

				newLatest, err := cs.fetcher.GetLatestBlock()
				if err != nil {
					slog.Error("Error getting latest block", "chain_id", cs.chainId, "error", err)
					continue
				}

				// Update chain status with latest block from RPC
				if err := chwrapper.UpdateLatestBlock(cs.conn, cs.chainId, cs.chainName, uint64(newLatest)); err != nil {
					slog.Error("Error updating chain status", "chain_id", cs.chainId, "error", err)
				}

				if newLatest > latestBlock {
					latestBlock = newLatest
				} else {
					continue
				}
			}

			// Calculate batch range
			endBlock := currentBlock + int64(cs.fetchBatchSize) - 1
			if endBlock > latestBlock {
				endBlock = latestBlock
			}

			// Fetch blocks
			blocks, err := cs.fetcher.FetchBlockRange(currentBlock, endBlock)
			if err != nil {
				slog.Error("Error fetching blocks", "chain_id", cs.chainId, "from", currentBlock, "to", endBlock, "error", err)
				time.Sleep(1 * time.Second)
				continue
			}

			// Update fetched counter
			cs.mu.Lock()
			cs.blocksFetched += int64(len(blocks))
			cs.mu.Unlock()

			// Send to channel (will block if buffer is full - backpressure)
			select {
			case cs.blockChan <- blocks:
				currentBlock = endBlock + 1
			case <-cs.ctx.Done():
				return
			}
		}
	}
}

// writerLoop is the consumer goroutine that writes to ClickHouse
func (cs *ChainSyncer) writerLoop() {
	defer cs.wg.Done()

	var buffer []*evmrpc.NormalizedBlock
	var lastFlushTime time.Time
	flushTimer := time.NewTimer(cs.flushInterval)
	defer flushTimer.Stop()

	// flush writes buffered blocks and ensures minimum interval between writes
	flush := func() time.Duration {
		if len(buffer) == 0 {
			return cs.flushInterval
		}

		start := time.Now()
		if err := cs.writeBlocks(buffer); err != nil {
			// Log error and retry - transient connection issues are handled by retry logic
			// If we get here, retries have been exhausted but we can try again on next flush
			slog.Error("Error writing blocks, will retry on next flush", "chain_id", cs.chainId, "error", err)
			return cs.flushInterval
		}

		elapsed := time.Since(start)
		if elapsed > 10*time.Second {
			slog.Warn("Write exceeded threshold", "chain_id", cs.chainId, "elapsed", elapsed)
		}

		// Update counters and clear buffer
		cs.mu.Lock()
		cs.blocksWritten += int64(len(buffer))
		cs.mu.Unlock()
		buffer = nil

		// Calculate next flush time to maintain minimum interval
		lastFlushTime = start
		nextFlush := cs.flushInterval - elapsed
		if nextFlush < 0 {
			nextFlush = 0
		}
		return nextFlush
	}

	for {
		select {
		case <-cs.ctx.Done():
			flush()
			return

		case blocks, ok := <-cs.blockChan:
			if !ok {
				flush()
				return
			}

			buffer = append(buffer, blocks...)

			// Flush immediately if interval has passed
			if !lastFlushTime.IsZero() && time.Since(lastFlushTime) >= cs.flushInterval {
				flushTimer.Stop()
				nextInterval := flush()
				flushTimer.Reset(nextInterval)
			}

		case <-flushTimer.C:
			nextInterval := flush()
			flushTimer.Reset(nextInterval)
		}
	}
}

// writeBlocks writes blocks to all tables in parallel and updates watermark
// Duplicate prevention strategy:
// 1. Start from watermark (guaranteed safe position where all tables have data)
// 2. Filter blocks by maxBlock for each table (only insert blocks > maxBlock)
// 3. Insert to all 4 tables in parallel - any failure causes panic
// 4. Update watermark only after ALL tables succeed - failure causes panic
// This ensures consistency: either all operations succeed or the app crashes
func (cs *ChainSyncer) writeBlocks(blocks []*evmrpc.NormalizedBlock) error {
	if len(blocks) == 0 {
		return nil
	}

	ctx := context.Background()
	g, ctx := errgroup.WithContext(ctx)
	start := time.Now()

	// Insert to blocks table
	g.Go(func() error {
		return InsertBlocks(ctx, cs.conn, cs.chainId, blocks, cs.maxBlockBlocks)
	})

	// Insert to transactions table
	g.Go(func() error {
		return InsertTransactions(ctx, cs.conn, cs.chainId, blocks, cs.maxBlockTransactions)
	})

	// Insert to traces table
	g.Go(func() error {
		return InsertTraces(ctx, cs.conn, cs.chainId, blocks, cs.maxBlockTraces)
	})

	// Insert to logs table
	g.Go(func() error {
		return InsertLogs(ctx, cs.conn, cs.chainId, blocks, cs.maxBlockLogs)
	})

	// Wait for all inserts to complete
	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed to insert blocks: %w", err)
	}

	elapsed := time.Since(start)
	txCount := 0
	for _, b := range blocks {
		txCount += len(b.Block.Transactions)
	}
	slog.Info("Inserted blocks", "chain_id", cs.chainId, "blocks", len(blocks), "txs", txCount, "elapsed", elapsed)

	// Update watermark to the highest block number in this batch
	maxBlock := uint32(0)
	for _, b := range blocks {
		blockNum, err := hexToUint32(b.Block.Number)
		if err != nil {
			continue
		}
		if blockNum > maxBlock {
			maxBlock = blockNum
		}
	}

	if maxBlock > cs.watermark {
		if err := chwrapper.SetWatermark(cs.conn, cs.chainId, maxBlock); err != nil {
			// Exit on watermark update failure - this is critical for preventing duplicates
			// If we can't update watermark after successful inserts, we risk data duplication on restart
			slog.Error("Failed to update watermark after successful inserts", "chain_id", cs.chainId, "error", err)
			os.Exit(1)
		}
		cs.watermark = maxBlock
	}

	// Update indexer runner with latest block info (only once per batch)
	if len(blocks) > 0 {
		// Find the latest block by number
		var latestBlock *evmrpc.NormalizedBlock
		latestBlockNum := uint32(0)
		for _, b := range blocks {
			blockNum, err := hexToUint32(b.Block.Number)
			if err != nil {
				continue
			}
			if blockNum > latestBlockNum {
				latestBlockNum = blockNum
				latestBlock = b
			}
		}

		if latestBlock != nil {
			// Convert hex timestamp to uint64
			timestamp, err := hexToUint64(latestBlock.Block.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to parse block timestamp: %w", err)
			}

			// Call OnBlock with block number and timestamp (skip in fast mode)
			if !cs.fast {
				blockTime := time.Unix(int64(timestamp), 0).UTC()
				cs.indexerRunner.OnBlock(uint64(latestBlockNum), blockTime)
			}
		}
	}

	return nil
}

// printProgress prints sync progress periodically
func (cs *ChainSyncer) printProgress() {
	defer cs.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-cs.ctx.Done():
			return
		case <-ticker.C:
			cs.mu.Lock()
			fetched := cs.blocksFetched
			written := cs.blocksWritten
			cs.mu.Unlock()

			elapsed := time.Since(cs.startTime)
			fetchRate := float64(fetched) / elapsed.Seconds()
			writeRate := float64(written) / elapsed.Seconds()
			lag := fetched - written

			slog.Info("Sync progress", "chain_id", cs.chainId, "fetched", fetched, "fetch_rate", fmt.Sprintf("%.1f/s", fetchRate), "written", written, "write_rate", fmt.Sprintf("%.1f/s", writeRate), "lag", lag, "watermark", cs.watermark)
		}
	}
}
