package chwrapper

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// GetWatermark returns the current watermark block number for a given chain, or 0 if not set
func GetWatermark(conn driver.Conn, chainId uint32) (uint32, error) {
	ctx := context.Background()

	query := "SELECT block_number FROM sync_watermark WHERE chain_id = ?"

	row := conn.QueryRow(ctx, query, chainId)
	var blockNumber uint32
	if err := row.Scan(&blockNumber); err != nil {
		// No watermark exists yet
		return 0, nil
	}

	return blockNumber, nil
}

// SetWatermark updates the watermark to the given block number for a specific chain
// Uses retry logic to handle transient connection errors
func SetWatermark(conn driver.Conn, chainId uint32, blockNumber uint32) error {
	ctx := context.Background()

	query := "INSERT INTO sync_watermark (chain_id, block_number) VALUES (?, ?)"

	if err := RetryableExec(ctx, conn, query, chainId, blockNumber); err != nil {
		return fmt.Errorf("failed to set watermark: %w", err)
	}

	return nil
}
