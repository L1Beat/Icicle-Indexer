package chwrapper

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Migration statements for adding bloom filter indexes to existing tables.
// These indexes dramatically speed up transaction lookups by hash (~36s -> <100ms).
//
// Run with: RunMigrations(conn)
// Or manually via clickhouse-client:
//   docker exec -it clickhouse-metrics clickhouse-client
//   Then paste each ALTER statement.

var bloomFilterMigrations = []string{
	// Add bloom filter index on raw_txs.hash for fast transaction lookup
	`ALTER TABLE raw_txs ADD INDEX IF NOT EXISTS idx_hash hash TYPE bloom_filter GRANULARITY 1`,
	`ALTER TABLE raw_txs MATERIALIZE INDEX idx_hash`,

	// Add bloom filter index on raw_traces.tx_hash for internal transactions query
	`ALTER TABLE raw_traces ADD INDEX IF NOT EXISTS idx_tx_hash tx_hash TYPE bloom_filter GRANULARITY 1`,
	`ALTER TABLE raw_traces MATERIALIZE INDEX idx_tx_hash`,

	// Add bloom filter index on raw_logs.transaction_hash for token transfers/approvals query
	`ALTER TABLE raw_logs ADD INDEX IF NOT EXISTS idx_transaction_hash transaction_hash TYPE bloom_filter GRANULARITY 1`,
	`ALTER TABLE raw_logs MATERIALIZE INDEX idx_transaction_hash`,
}

// RunMigrations applies all pending migrations to add bloom filter indexes.
// Safe to run multiple times - uses IF NOT EXISTS.
func RunMigrations(conn driver.Conn) error {
	ctx := context.Background()

	for _, stmt := range bloomFilterMigrations {
		if err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migration failed on statement %q: %w", stmt, err)
		}
	}

	return nil
}
