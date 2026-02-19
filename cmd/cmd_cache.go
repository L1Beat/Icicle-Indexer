package cmd

import (
	"icicle/pkg/cache"
	"fmt"
	"log"
	"log/slog"
	"sync"

	"icicle/pkg/evmrpc"
	"icicle/pkg/pchainrpc"
	"sync/atomic"
	"time"

	"github.com/dustin/go-humanize"
)

func RunCache() {
	slog.Info("Starting cache-only mode (no ClickHouse)")

	// Load configuration from YAML
	configs, err := LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if len(configs) == 0 {
		log.Fatal("No chain configurations found in config.yaml")
	}

	var wg sync.WaitGroup

	// Start a cacher for each chain
	for _, cfg := range configs {
		wg.Add(1)
		go func(chainCfg ChainConfig) {
			defer wg.Done()

			var err error
			switch chainCfg.VM {
			case "evm":
				err = runEVMCache(chainCfg)
			case "p":
				err = runPChainCache(chainCfg)
			default:
				slog.Error("Unsupported VM type", "chain_id", chainCfg.ChainID, "vm", chainCfg.VM)
				return
			}

			if err != nil {
				slog.Error("Cache failed", "chain_id", chainCfg.ChainID, "chain_name", chainCfg.Name, "error", err)
			}
		}(cfg)
	}

	wg.Wait()
}

func runEVMCache(cfg ChainConfig) error {
	// Defaults
	maxConcurrency := cfg.MaxConcurrency
	if maxConcurrency == 0 {
		maxConcurrency = 100
	}
	fetchBatchSize := cfg.FetchBatchSize
	if fetchBatchSize == 0 {
		fetchBatchSize = 1000
	}

	slog.Info("Creating cache", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "path", fmt.Sprintf("./rpc_cache/%d", cfg.ChainID))
	cacheInstance, err := cache.New("./rpc_cache", cfg.ChainID)
	if err != nil {
		return fmt.Errorf("failed to create cache: %w", err)
	}
	defer cacheInstance.Close()

	// Check for existing checkpoint
	checkpoint, err := cacheInstance.GetCheckpoint()
	if err != nil {
		return fmt.Errorf("failed to read checkpoint: %w", err)
	}

	if checkpoint > 0 {
		slog.Info("Found checkpoint, resuming", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "checkpoint_block", checkpoint)
	}

	slog.Info("Creating fetcher", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "concurrency", maxConcurrency, "batch_size", fetchBatchSize)
	fetcher := evmrpc.NewFetcher(evmrpc.FetcherOptions{
		RpcURL:         cfg.RpcURL,
		ChainID:        cfg.ChainID,
		ChainName:      cfg.Name,
		MaxConcurrency: maxConcurrency,
		MaxRetries:     100,
		RetryDelay:     100 * time.Millisecond,
		BatchSize:      fetchBatchSize,
		DebugBatchSize: 1,
		Cache:          cacheInstance,
	})
	defer fetcher.Close()

	// Get latest block from RPC
	latestBlock, err := fetcher.GetLatestBlock()
	if err != nil {
		return fmt.Errorf("failed to get latest block: %w", err)
	}

	originalStartBlock := cfg.StartBlock
	if originalStartBlock == 0 {
		originalStartBlock = 1
	}

	startBlock := originalStartBlock
	// Resume from checkpoint if it's ahead
	if checkpoint > startBlock {
		startBlock = checkpoint + 1
	}

	// For cache mode, always cache up to the latest block
	endBlock := latestBlock

	if startBlock > endBlock {
		slog.Info("Already caught up, nothing to do", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "end_block", endBlock)
		select {} // Block forever
	}

	totalBlocks := endBlock - originalStartBlock + 1
	remainingBlocks := endBlock - startBlock + 1
	if checkpoint > 0 {
		slog.Info("Resuming cache", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "start_block", startBlock, "end_block", endBlock, "remaining", humanize.Comma(remainingBlocks), "total", humanize.Comma(totalBlocks))
	} else {
		slog.Info("Caching blocks", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "start_block", startBlock, "end_block", endBlock, "total", humanize.Comma(totalBlocks))
	}

	// Progress tracking
	var blocksCached atomic.Int64
	startTime := time.Now()
	alreadyCached := startBlock - originalStartBlock // blocks already done from previous runs

	// Progress printer
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				cached := blocksCached.Load()
				totalCachedSoFar := alreadyCached + cached
				elapsed := time.Since(startTime)
				rate := float64(cached) / elapsed.Seconds()
				progress := float64(totalCachedSoFar) / float64(totalBlocks) * 100

				blocksRemaining := totalBlocks - totalCachedSoFar
				var eta string
				if rate > 0 && blocksRemaining > 0 {
					etaSeconds := float64(blocksRemaining) / rate
					etaDuration := time.Duration(etaSeconds * float64(time.Second))
					eta = etaDuration.Round(time.Second).String()
				}

				slog.Info("Cache progress", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "cached", humanize.Comma(totalCachedSoFar), "total", humanize.Comma(totalBlocks), "progress", fmt.Sprintf("%.1f%%", progress), "rate", fmt.Sprintf("%.1f/s", rate), "elapsed", elapsed.Round(time.Second), "eta", eta)
			case <-done:
				return
			}
		}
	}()

	// Fetch in parallel chunks
	chunkSize := int64(fetchBatchSize)
	var fetchWg sync.WaitGroup

	// Limit concurrent fetch operations (each has internal concurrency via MaxConcurrency)
	semaphore := make(chan struct{}, 10)

	// Track highest block cached for checkpoint
	var highestBlockMu sync.Mutex
	highestBlock := startBlock - 1
	checkpointInterval := int64(1000) // Save checkpoint every 1k blocks
	lastCheckpoint := highestBlock

	for current := startBlock; current <= endBlock; {
		batchEnd := current + chunkSize - 1
		if batchEnd > endBlock {
			batchEnd = endBlock
		}

		fetchWg.Add(1)
		semaphore <- struct{}{}

		go func(from, to int64) {
			defer fetchWg.Done()
			defer func() { <-semaphore }()

			blocks, err := fetcher.FetchBlockRange(from, to)
			if err != nil {
				slog.Error("Error fetching blocks", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "from", from, "to", to, "error", err)
				return
			}

			blocksCached.Add(int64(len(blocks)))

			// Update highest block and checkpoint if needed
			highestBlockMu.Lock()
			if to > highestBlock {
				highestBlock = to
			}

			// Save checkpoint every interval
			if highestBlock-lastCheckpoint >= checkpointInterval {
				if err := cacheInstance.SetCheckpoint(highestBlock); err != nil {
					slog.Error("Failed to save checkpoint", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "block", highestBlock, "error", err)
				} else {
					slog.Info("Checkpoint saved", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "block", humanize.Comma(int64(highestBlock)))
					lastCheckpoint = highestBlock
				}
			}
			highestBlockMu.Unlock()
		}(current, batchEnd)

		current = batchEnd + 1
	}

	fetchWg.Wait()
	close(done)

	// Save checkpoint after initial sync
	if err := cacheInstance.SetCheckpoint(endBlock); err != nil {
		slog.Error("Failed to save checkpoint", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "error", err)
	} else {
		slog.Info("Checkpoint saved", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "block", endBlock)
	}

	elapsed := time.Since(startTime)
	finalCount := blocksCached.Load()
	avgRate := float64(finalCount) / elapsed.Seconds()

	slog.Info("Initial sync complete", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "blocks_cached", finalCount, "elapsed", elapsed.Round(time.Second), "avg_rate", fmt.Sprintf("%.1f/s", avgRate))

	// Show cache metrics
	slog.Info("Cache metrics", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "metrics", cacheInstance.GetMetrics())

	// Done caching, block forever
	slog.Info("Cache complete, nothing more to do", "chain_id", cfg.ChainID, "chain_name", cfg.Name)
	select {} // Block forever
}

func runPChainCache(cfg ChainConfig) error {
	// Defaults
	maxConcurrency := cfg.MaxConcurrency
	if maxConcurrency == 0 {
		maxConcurrency = 100
	}
	fetchBatchSize := cfg.FetchBatchSize
	if fetchBatchSize == 0 {
		fetchBatchSize = 1000
	}

	slog.Info("Creating cache", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "path", fmt.Sprintf("./rpc_cache/%d", cfg.ChainID))
	cacheInstance, err := cache.New("./rpc_cache", cfg.ChainID)
	if err != nil {
		return fmt.Errorf("failed to create cache: %w", err)
	}
	defer cacheInstance.Close()

	// Check for existing checkpoint
	checkpoint, err := cacheInstance.GetCheckpoint()
	if err != nil {
		return fmt.Errorf("failed to read checkpoint: %w", err)
	}

	if checkpoint > 0 {
		slog.Info("Found checkpoint, resuming", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "checkpoint_block", checkpoint)
	}

	slog.Info("Creating fetcher", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "concurrency", maxConcurrency, "batch_size", fetchBatchSize)
	fetcher := pchainrpc.NewFetcher(pchainrpc.FetcherOptions{
		RpcURL:         cfg.RpcURL,
		MaxConcurrency: maxConcurrency,
		MaxRetries:     100,
		RetryDelay:     100 * time.Millisecond,
		BatchSize:      fetchBatchSize,
		Cache:          cacheInstance,
	})
	defer fetcher.Close()

	// Get latest block from RPC
	latestBlock, err := fetcher.GetLatestBlock()
	if err != nil {
		return fmt.Errorf("failed to get latest block: %w", err)
	}

	originalStartBlock := cfg.StartBlock
	if originalStartBlock == 0 {
		originalStartBlock = 1
	}

	startBlock := originalStartBlock
	// Resume from checkpoint if it's ahead
	if checkpoint > startBlock {
		startBlock = checkpoint + 1
	}

	// For cache mode, always cache up to the latest block
	endBlock := latestBlock

	if startBlock > endBlock {
		slog.Info("Already caught up, nothing to do", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "end_block", endBlock)
		select {} // Block forever
	}

	totalBlocks := endBlock - originalStartBlock + 1
	remainingBlocks := endBlock - startBlock + 1
	if checkpoint > 0 {
		slog.Info("Resuming cache", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "start_block", startBlock, "end_block", endBlock, "remaining", humanize.Comma(remainingBlocks), "total", humanize.Comma(totalBlocks))
	} else {
		slog.Info("Caching blocks", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "start_block", startBlock, "end_block", endBlock, "total", humanize.Comma(totalBlocks))
	}

	// Progress tracking
	var blocksCached atomic.Int64
	startTime := time.Now()
	alreadyCached := startBlock - originalStartBlock // blocks already done from previous runs

	// Progress printer
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				cached := blocksCached.Load()
				totalCachedSoFar := alreadyCached + cached
				elapsed := time.Since(startTime)
				rate := float64(cached) / elapsed.Seconds()
				progress := float64(totalCachedSoFar) / float64(totalBlocks) * 100

				blocksRemaining := totalBlocks - totalCachedSoFar
				var eta string
				if rate > 0 && blocksRemaining > 0 {
					etaSeconds := float64(blocksRemaining) / rate
					etaDuration := time.Duration(etaSeconds * float64(time.Second))
					eta = etaDuration.Round(time.Second).String()
				}

				slog.Info("Cache progress", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "cached", humanize.Comma(totalCachedSoFar), "total", humanize.Comma(totalBlocks), "progress", fmt.Sprintf("%.1f%%", progress), "rate", fmt.Sprintf("%.1f/s", rate), "elapsed", elapsed.Round(time.Second), "eta", eta)
			case <-done:
				return
			}
		}
	}()

	// Fetch in parallel chunks
	chunkSize := int64(fetchBatchSize)
	var fetchWg sync.WaitGroup

	// Limit concurrent fetch operations (each has internal concurrency via MaxConcurrency)
	semaphore := make(chan struct{}, 10)

	// Track highest block cached for checkpoint
	var highestBlockMu sync.Mutex
	highestBlock := startBlock - 1
	checkpointInterval := int64(100000) // Save checkpoint every 10k blocks
	lastCheckpoint := highestBlock

	for current := startBlock; current <= endBlock; {
		batchEnd := current + chunkSize - 1
		if batchEnd > endBlock {
			batchEnd = endBlock
		}

		fetchWg.Add(1)
		semaphore <- struct{}{}

		go func(from, to int64) {
			defer fetchWg.Done()
			defer func() { <-semaphore }()

			blocks, err := fetcher.FetchBlockRange(from, to)
			if err != nil {
				slog.Error("Error fetching blocks", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "from", from, "to", to, "error", err)
				return
			}

			blocksCached.Add(int64(len(blocks)))

			// Update highest block and checkpoint if needed
			highestBlockMu.Lock()
			if to > highestBlock {
				highestBlock = to
			}

			// Save checkpoint every interval
			if highestBlock-lastCheckpoint >= checkpointInterval {
				if err := cacheInstance.SetCheckpoint(highestBlock); err != nil {
					slog.Error("Failed to save checkpoint", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "block", highestBlock, "error", err)
				} else {
					slog.Info("Checkpoint saved", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "block", humanize.Comma(int64(highestBlock)))
					lastCheckpoint = highestBlock
				}
			}
			highestBlockMu.Unlock()
		}(current, batchEnd)

		current = batchEnd + 1
	}

	fetchWg.Wait()
	close(done)

	// Save checkpoint after initial sync
	if err := cacheInstance.SetCheckpoint(endBlock); err != nil {
		slog.Error("Failed to save checkpoint", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "error", err)
	} else {
		slog.Info("Checkpoint saved", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "block", endBlock)
	}

	elapsed := time.Since(startTime)
	finalCount := blocksCached.Load()
	avgRate := float64(finalCount) / elapsed.Seconds()

	slog.Info("Initial sync complete", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "blocks_cached", finalCount, "elapsed", elapsed.Round(time.Second), "avg_rate", fmt.Sprintf("%.1f/s", avgRate))

	// Show cache metrics
	slog.Info("Cache metrics", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "metrics", cacheInstance.GetMetrics())

	// Done caching, block forever
	slog.Info("Cache complete, nothing more to do", "chain_id", cfg.ChainID, "chain_name", cfg.Name)
	select {} // Block forever
}
