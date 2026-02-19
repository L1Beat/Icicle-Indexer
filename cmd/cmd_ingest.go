package cmd

import (
	"context"
	"icicle/pkg/cache"
	"icicle/pkg/chwrapper"
	"icicle/pkg/registrysyncer"
	"log"
	"log/slog"
	"sync"
)

func RunIngest(ctx context.Context, fast bool) {
	if fast {
		slog.Info("Starting ingest in FAST mode (indexers disabled)")
	} else {
		slog.Info("Starting ingest")
	}

	// Load configuration from YAML
	configs, err := LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if len(configs) == 0 {
		log.Fatal("No chain configurations found in config.yaml")
	}

	// Connect to ClickHouse
	conn, err := chwrapper.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}
	defer conn.Close()

	err = chwrapper.CreateTables(conn)
	if err != nil {
		log.Fatalf("Failed to create tables: %v", err)
	}

	// Sync L1 Registry at startup (in background)
	go func() {
		if err := registrysyncer.SyncRegistry(ctx, conn); err != nil {
			slog.Error("Failed to sync L1 registry", "error", err)
		}
	}()

	var wg sync.WaitGroup
	var syncers []Syncer

	// Start a syncer for each chain
	for _, cfg := range configs {
		// Create cache
		cacheInstance, err := cache.New("./rpc_cache", cfg.ChainID)
		if err != nil {
			log.Fatalf("Failed to create cache for chain %d: %v", cfg.ChainID, err)
		}
		defer cacheInstance.Close()

		// Create syncer based on VM type
		syncer, err := CreateSyncer(cfg, conn, cacheInstance, fast)
		if err != nil {
			log.Fatalf("Failed to create syncer for chain %d (%s): %v", cfg.ChainID, cfg.VM, err)
		}
		syncers = append(syncers, syncer)

		wg.Add(1)
		go func(s Syncer, chainID uint32, chainName string) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("PANIC RECOVERED", "chain_id", chainID, "error", r)
				}
				slog.Info("Syncer goroutine exiting", "chain_id", chainID)
				wg.Done()
			}()
			if err := s.Start(); err != nil {
				slog.Error("Failed to start syncer", "chain_id", chainID, "chain_name", chainName, "error", err)
			}
			s.Wait()
			slog.Info("Wait() returned - syncer stopped", "chain_id", chainID)
		}(syncer, cfg.ChainID, cfg.Name)

		slog.Info("Started syncer", "chain_id", cfg.ChainID, "chain_name", cfg.Name, "vm", cfg.VM)
	}

	// Wait for shutdown signal
	<-ctx.Done()
	slog.Info("Shutdown signal received - stopping all syncers")

	// Stop all syncers gracefully
	for _, s := range syncers {
		s.Stop()
	}

	wg.Wait()
	slog.Info("All syncers stopped - RunIngest() returning")
}
