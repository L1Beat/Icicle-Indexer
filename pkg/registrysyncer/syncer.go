package registrysyncer

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

const (
	RegistryRepoURL = "https://github.com/L1Beat/l1-registry.git"
	TempDirPrefix   = "l1-registry-sync"
)

// SyncRegistry clones the L1 registry and ingests metadata into ClickHouse
func SyncRegistry(ctx context.Context, conn clickhouse.Conn) error {
	slog.Info("Starting L1 registry sync")

	// Create temp dir
	tempDir, err := os.MkdirTemp("", TempDirPrefix)
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Clone repo
	slog.Info("Cloning registry repo", "url", RegistryRepoURL, "dir", tempDir)
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth=1", RegistryRepoURL, tempDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %s: %w", string(output), err)
	}

	// Walk data directory
	dataDir := filepath.Join(tempDir, "data")
	var chains []ChainRegistry

	err = filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) != "chain.json" {
			return nil
		}

		// Read and parse chain.json
		content, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("Failed to read registry file", "path", path, "error", err)
			return nil
		}

		var chain ChainRegistry
		if err := json.Unmarshal(content, &chain); err != nil {
			slog.Warn("Failed to parse registry file", "path", path, "error", err)
			return nil
		}

		// Only keep mainnet chains or those with valid subnet IDs
		if chain.SubnetID != "" {
			chains = append(chains, chain)
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk data dir: %w", err)
	}

	slog.Info("Found chains metadata", "count", len(chains))

	// Insert into ClickHouse
	if len(chains) > 0 {
		if err := insertRegistryData(ctx, conn, chains); err != nil {
			return fmt.Errorf("failed to insert registry data: %w", err)
		}
	}

	slog.Info("Registry sync completed successfully")
	return nil
}

func insertRegistryData(ctx context.Context, conn clickhouse.Conn, chains []ChainRegistry) error {
	batch, err := conn.PrepareBatch(ctx, `INSERT INTO l1_registry (
		subnet_id, name, description, logo_url, website_url, last_updated
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare batch: %w", err)
	}

	now := time.Now()
	for _, chain := range chains {
		err = batch.Append(
			chain.SubnetID,
			chain.Name,
			chain.Description,
			chain.Logo,
			chain.Website,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to append chain %s: %w", chain.Name, err)
		}
	}

	return batch.Send()
}

