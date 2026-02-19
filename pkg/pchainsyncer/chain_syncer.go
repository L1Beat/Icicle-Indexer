package pchainsyncer

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"icicle/pkg/cache"
	"icicle/pkg/chwrapper"
	"icicle/pkg/pchainrpc"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

const (
	// DefaultPChainBufferSize is the maximum number of batches that can be buffered in the channel
	DefaultPChainBufferSize = 10000
	// DefaultPChainFlushInterval is how often to flush blocks to ClickHouse
	DefaultPChainFlushInterval = 1 * time.Second
	// DefaultPChainMaxRetries is the default max retries for RPC requests
	DefaultPChainMaxRetries = 20
	// DefaultPChainRetryDelay is the default initial retry delay
	DefaultPChainRetryDelay = 100 * time.Millisecond
)

// Config holds configuration for PChainSyncer
type Config struct {
	ChainID        uint32
	RpcURL         string
	StartBlock     int64         // Starting block number when no watermark exists
	MaxConcurrency int           // Maximum concurrent RPC requests
	FetchBatchSize int           // Blocks per fetch
	BufferSize     int           // Channel buffer size, default 10000
	FlushInterval  time.Duration // Flush interval, default 1s
	MaxRetries     int           // Max retries for RPC requests, default 20
	RetryDelay     time.Duration // Initial retry delay, default 100ms
	CHConn         driver.Conn   // ClickHouse connection
	Cache          *cache.Cache  // Cache for RPC calls
	Name           string        // Chain name for display

	// Validator syncer config
	EnableValidatorSync   bool          // Enable L1 validator state syncing
	ValidatorSyncInterval time.Duration // How often to sync validator state (default: 5min)
}

// PChainSyncer manages P-chain sync
type PChainSyncer struct {
	chainID        uint32
	chainName      string
	fetcher        *pchainrpc.Fetcher
	conn           driver.Conn
	blockChan      chan []*pchainrpc.JSONBlock // Bounded channel for backpressure
	watermark      uint64                      // Current sync position
	startBlock     int64                       // Starting block when no watermark
	fetchBatchSize int
	flushInterval  time.Duration

	// Validator syncer
	validatorSyncer *ValidatorSyncer

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Progress tracking
	mu            sync.Mutex
	blocksFetched int64
	blocksWritten int64
	lastPrintTime time.Time
	startTime     time.Time
}

// NewPChainSyncer creates a new P-chain syncer
func NewPChainSyncer(cfg Config) (*PChainSyncer, error) {
	if cfg.FetchBatchSize == 0 {
		cfg.FetchBatchSize = 100
	}
	if cfg.MaxConcurrency == 0 {
		cfg.MaxConcurrency = 50
	}
	if cfg.StartBlock == 0 {
		cfg.StartBlock = 1
	}
	if cfg.Name == "" {
		cfg.Name = fmt.Sprintf("P-Chain-%d", cfg.ChainID)
	}
	if cfg.BufferSize == 0 {
		cfg.BufferSize = DefaultPChainBufferSize
	}
	if cfg.FlushInterval == 0 {
		cfg.FlushInterval = DefaultPChainFlushInterval
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = DefaultPChainMaxRetries
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = DefaultPChainRetryDelay
	}

	// Create fetcher
	fetcher := pchainrpc.NewFetcher(pchainrpc.FetcherOptions{
		RpcURL:         cfg.RpcURL,
		MaxConcurrency: cfg.MaxConcurrency,
		MaxRetries:     cfg.MaxRetries,
		RetryDelay:     cfg.RetryDelay,
		BatchSize:      cfg.FetchBatchSize,
		Cache:          cfg.Cache,
	})

	ctx, cancel := context.WithCancel(context.Background())

	ps := &PChainSyncer{
		chainID:        cfg.ChainID,
		chainName:      cfg.Name,
		fetcher:        fetcher,
		conn:           cfg.CHConn,
		blockChan:      make(chan []*pchainrpc.JSONBlock, cfg.BufferSize),
		startBlock:     cfg.StartBlock,
		fetchBatchSize: cfg.FetchBatchSize,
		flushInterval:  cfg.FlushInterval,
		ctx:            ctx,
		cancel:         cancel,
		lastPrintTime:  time.Now(),
		startTime:      time.Now(),
	}

	// Create validator syncer if enabled
	if cfg.EnableValidatorSync {
		ps.validatorSyncer = NewValidatorSyncer(
			ValidatorSyncerConfig{
				PChainID:      cfg.ChainID,
				SyncInterval:  cfg.ValidatorSyncInterval,
				DiscoveryMode: "auto",
			},
			fetcher,
			cfg.CHConn,
		)
	}

	return ps, nil
}

// Start begins syncing
func (ps *PChainSyncer) Start() error {
	slog.Info("Starting syncer", "chain_id", ps.chainID, "chain_name", ps.chainName)

	// Get starting position
	startBlock, err := ps.getStartingBlock()
	if err != nil {
		return fmt.Errorf("failed to determine starting block: %w", err)
	}

	slog.Info("Starting from block", "chain_id", ps.chainID, "chain_name", ps.chainName, "start_block", startBlock)

	// Get latest block from RPC
	latestBlock, err := ps.fetcher.GetLatestBlock()
	if err != nil {
		return fmt.Errorf("failed to get latest block: %w", err)
	}

	slog.Info("Latest block on chain", "chain_id", ps.chainID, "chain_name", ps.chainName, "latest_block", latestBlock)

	// Initialize chain status in database
	if err := chwrapper.UpsertChainStatus(ps.conn, ps.chainID, ps.chainName, uint64(latestBlock)); err != nil {
		return fmt.Errorf("failed to upsert chain status: %w", err)
	}

	// Start producer (fetcher) goroutine
	ps.wg.Add(1)
	go ps.fetcherLoop(startBlock, latestBlock)

	// Start consumer (writer) goroutine
	ps.wg.Add(1)
	go ps.writerLoop()

	// Start progress printer
	ps.wg.Add(1)
	go ps.printProgress()

	// Start validator syncer if enabled
	if ps.validatorSyncer != nil {
		ps.wg.Add(1)
		go func() {
			defer ps.wg.Done()
			ps.validatorSyncer.Start(ps.ctx)
		}()
	}

	return nil
}

// Stop gracefully shuts down the syncer
func (ps *PChainSyncer) Stop() {
	slog.Info("Stopping syncer", "chain_id", ps.chainID, "chain_name", ps.chainName)

	// Stop validator syncer first
	if ps.validatorSyncer != nil {
		ps.validatorSyncer.Stop()
	}

	ps.cancel()
	close(ps.blockChan)
	ps.wg.Wait()
	slog.Info("Syncer stopped", "chain_id", ps.chainID, "chain_name", ps.chainName)
}

// Wait blocks until syncer completes
func (ps *PChainSyncer) Wait() {
	ps.wg.Wait()
}

// getStartingBlock determines where to start syncing from
func (ps *PChainSyncer) getStartingBlock() (int64, error) {
	// Get watermark
	watermark, err := chwrapper.GetWatermark(ps.conn, ps.chainID)
	if err != nil {
		return 0, fmt.Errorf("failed to get watermark: %w", err)
	}
	ps.watermark = uint64(watermark)

	// If no watermark, start from configured start block
	if watermark == 0 {
		return ps.startBlock, nil
	}

	// Start from watermark+1
	return int64(watermark + 1), nil
}

// fetcherLoop is the producer goroutine that fetches blocks
func (ps *PChainSyncer) fetcherLoop(startBlock, latestBlock int64) {
	defer ps.wg.Done()

	currentBlock := startBlock

	for {
		select {
		case <-ps.ctx.Done():
			return
		default:
			// Check if we're caught up
			if currentBlock > latestBlock {
				// Poll for new blocks (500ms for near real-time updates)
				time.Sleep(500 * time.Millisecond)

				newLatest, err := ps.fetcher.GetLatestBlock()
				if err != nil {
					slog.Error("Error getting latest block", "chain_id", ps.chainID, "chain_name", ps.chainName, "error", err)
					continue
				}

				// Update chain status with latest block from RPC
				if err := chwrapper.UpdateLatestBlock(ps.conn, ps.chainID, ps.chainName, uint64(newLatest)); err != nil {
					slog.Error("Error updating chain status", "chain_id", ps.chainID, "chain_name", ps.chainName, "error", err)
				}

				if newLatest > latestBlock {
					latestBlock = newLatest
				} else {
					continue
				}
			}

			// Calculate batch range
			endBlock := currentBlock + int64(ps.fetchBatchSize) - 1
			if endBlock > latestBlock {
				endBlock = latestBlock
			}

			// Fetch blocks
			blocks, err := ps.fetcher.FetchBlockRangeJSON(currentBlock, endBlock)
			if err != nil {
				slog.Error("Error fetching blocks", "chain_id", ps.chainID, "chain_name", ps.chainName, "from", currentBlock, "to", endBlock, "error", err)
				time.Sleep(1 * time.Second)
				continue
			}

			// Update fetched counter
			ps.mu.Lock()
			ps.blocksFetched += int64(len(blocks))
			ps.mu.Unlock()

			// Send to channel (will block if buffer is full - backpressure)
			select {
			case ps.blockChan <- blocks:
				currentBlock = endBlock + 1
			case <-ps.ctx.Done():
				return
			}
		}
	}
}

// writerLoop is the consumer goroutine that writes to ClickHouse
func (ps *PChainSyncer) writerLoop() {
	defer ps.wg.Done()

	var buffer []*pchainrpc.JSONBlock
	var lastFlushTime time.Time
	flushTimer := time.NewTimer(ps.flushInterval)
	defer flushTimer.Stop()

	// flush writes buffered blocks and ensures minimum interval between writes
	flush := func() time.Duration {
		if len(buffer) == 0 {
			return ps.flushInterval
		}

		start := time.Now()
		if err := ps.writeBlocks(buffer); err != nil {
			slog.Error("Error writing blocks", "chain_id", ps.chainID, "chain_name", ps.chainName, "error", err)
			return ps.flushInterval
		}

		elapsed := time.Since(start)
		if elapsed > 10*time.Second {
			slog.Warn("Write exceeded threshold", "chain_id", ps.chainID, "chain_name", ps.chainName, "elapsed", elapsed)
		}

		// Update counters and clear buffer
		ps.mu.Lock()
		ps.blocksWritten += int64(len(buffer))
		ps.mu.Unlock()
		buffer = nil

		// Calculate next flush time to maintain minimum interval
		lastFlushTime = start
		nextFlush := ps.flushInterval - elapsed
		if nextFlush < 0 {
			nextFlush = 0
		}
		return nextFlush
	}

	for {
		select {
		case <-ps.ctx.Done():
			flush()
			return

		case blocks, ok := <-ps.blockChan:
			if !ok {
				flush()
				return
			}

			buffer = append(buffer, blocks...)

			// Flush immediately if interval has passed
			if !lastFlushTime.IsZero() && time.Since(lastFlushTime) >= ps.flushInterval {
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

// writeBlocks writes blocks to ClickHouse and updates watermark
func (ps *PChainSyncer) writeBlocks(blocks []*pchainrpc.JSONBlock) error {
	if len(blocks) == 0 {
		return nil
	}

	start := time.Now()

	// Insert transactions
	if err := InsertPChainTxs(ps.ctx, ps.conn, ps.chainID, blocks); err != nil {
		return fmt.Errorf("failed to insert P-chain txs: %w", err)
	}

	elapsed := time.Since(start)
	txCount := 0
	for _, b := range blocks {
		txCount += len(b.Transactions)
	}
	slog.Info("Inserted blocks", "chain_id", ps.chainID, "chain_name", ps.chainName, "blocks", len(blocks), "txs", txCount, "elapsed", elapsed)

	// Update watermark to the highest block number in this batch
	maxBlock := uint64(0)
	for _, b := range blocks {
		if b.Height > maxBlock {
			maxBlock = b.Height
		}
	}

	if maxBlock > ps.watermark {
		if err := chwrapper.SetWatermark(ps.conn, ps.chainID, uint32(maxBlock)); err != nil {
			return fmt.Errorf("failed to update watermark: %w", err)
		}
		ps.watermark = maxBlock
	}

	return nil
}

// printProgress prints sync progress periodically
func (ps *PChainSyncer) printProgress() {
	defer ps.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ps.ctx.Done():
			return
		case <-ticker.C:
			ps.mu.Lock()
			fetched := ps.blocksFetched
			written := ps.blocksWritten
			ps.mu.Unlock()

			elapsed := time.Since(ps.startTime)
			fetchRate := float64(fetched) / elapsed.Seconds()
			writeRate := float64(written) / elapsed.Seconds()
			lag := fetched - written

			slog.Info("Sync progress", "chain_id", ps.chainID, "chain_name", ps.chainName, "fetched", fetched, "fetch_rate", fmt.Sprintf("%.1f/s", fetchRate), "written", written, "write_rate", fmt.Sprintf("%.1f/s", writeRate), "lag", lag, "watermark", ps.watermark)
		}
	}
}
