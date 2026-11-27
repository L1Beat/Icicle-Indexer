package chwrapper

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// indexerWatermarksDDL is the one definition of indexer_watermarks, the shared
// progress table. granularity is a metric period ("hour", "day"), "time" for
// the C-Chain rollups, "block" for the lending discovery cursors, or empty for
// the EVM incrementals. Time-based progress goes in last_period, block-based
// progress in last_block_num.
const indexerWatermarksDDL = `
CREATE TABLE IF NOT EXISTS indexer_watermarks (
    chain_id UInt32,
    indexer_name String,
    granularity LowCardinality(String),
    last_period DateTime64(3, 'UTC'),
    last_block_num UInt64,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, indexer_name, granularity)`

// CreateIndexerWatermarks creates indexer_watermarks if it does not exist. Both
// the indexer and the lending service call it, since either may start first.
func CreateIndexerWatermarks(ctx context.Context, conn driver.Conn) error {
	wctx, cancel := WriteContext(ctx)
	defer cancel()
	return conn.Exec(wctx, indexerWatermarksDDL)
}

// ReadPeriodWatermark returns last_period of the indexer_watermarks row for
// (chainID, name), in UTC, or the zero time when there is none. Every
// granularity matches: each caller writes one granularity per name.
func ReadPeriodWatermark(ctx context.Context, conn driver.Conn, chainID uint32, name string) (time.Time, error) {
	var t time.Time
	rows, err := conn.Query(ctx, `
		SELECT last_period FROM indexer_watermarks FINAL
		WHERE chain_id = ? AND indexer_name = ?`, chainID, name)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(&t); err != nil {
			return t, err
		}
	}
	return t.UTC(), rows.Err()
}

// WriteWatermark writes one indexer_watermarks row. updated_at defaults to
// now, so the newest write wins once the table merges.
func WriteWatermark(ctx context.Context, conn driver.Conn, chainID uint32, name, granularity string, period time.Time, block uint64) error {
	wctx, cancel := WriteContext(ctx)
	defer cancel()
	return conn.Exec(wctx, `
		INSERT INTO indexer_watermarks (chain_id, indexer_name, granularity, last_period, last_block_num)
		VALUES (?, ?, ?, ?, ?)`, chainID, name, granularity, period, block)
}
