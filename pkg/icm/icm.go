// Package icm copies C-Chain ICM (Teleporter) messages out of raw_logs: one row
// per message sent from the C-Chain and per message received on it, keyed by
// messageID within each direction. Only the official TeleporterMessenger is
// read, since any contract can emit an event with the same signature.
// Messages between two L1s that never touch the C-Chain are not seen here.
package icm

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"icicle/pkg/chwrapper"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ChainID is the C-Chain.
const ChainID = 43114

// Messenger is the TeleporterMessenger, at the same address on every chain.
const Messenger = "253b2784c75e510dd0ff1da844684a1ac0aa5fcf"

// Event topics, from ITeleporterMessenger in ava-labs/icm-contracts (the same
// in v1.0.0 and main). TeleporterMessage is
// (uint256,address,bytes32,address,uint256,address[],(uint256,address)[],bytes).
const (
	// SendCrossChainMessage(bytes32 indexed messageID, bytes32 indexed
	// destinationBlockchainID, TeleporterMessage, (address,uint256) feeInfo)
	topicSend = "2a211ad4a59ab9d003852404f9c57c690704ee755f3c79d2c2812ad32da99df8"
	// ReceiveCrossChainMessage(bytes32 indexed messageID, bytes32 indexed
	// sourceBlockchainID, address indexed deliverer, address rewardRedeemer,
	// TeleporterMessage)
	topicReceive = "292ee90bbaf70b5d4936025e09d56ba08f3e421156b6a568cf3c2840d9343e34"
)

// Start is the UTC day the TeleporterMessenger was deployed on the C-Chain
// (block 42526120, 2024-03-05 22:13 UTC).
var Start = time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC)

// windowDays bounds one raw_logs scan. The scan reads one contract's two
// events, so a window is cheap.
const windowDays = 30

// budget bounds the work per Sync call, so the history fill does not hold up
// the C-Chain snapshot cycle for long.
const budget = time.Minute

// watermark names the indexer_watermarks row holding the time up to which
// raw_logs have been copied (last_period, exclusive).
const watermark = "icm/through"

// Sync copies messages from the watermark up to the C-Chain sync watermark, a
// window at a time, within budget.
func Sync(ctx context.Context, conn driver.Conn) error {
	var synced time.Time
	if err := conn.QueryRow(ctx, `
		SELECT block_time FROM raw_blocks
		WHERE chain_id = ? AND block_number = (SELECT block_number FROM sync_watermark WHERE chain_id = ?)
	`, uint32(ChainID), uint32(ChainID)).Scan(&synced); err != nil {
		return fmt.Errorf("sync watermark time: %w", err)
	}
	synced = synced.UTC()

	from, err := Through(ctx, conn)
	if err != nil {
		return err
	}
	if from.IsZero() {
		from = Start
	}

	deadline := time.Now().Add(budget)
	for from.Before(synced) && time.Now().Before(deadline) {
		end := from.AddDate(0, 0, windowDays)
		if end.After(synced) {
			end = synced
		}
		start := time.Now()
		wctx, cancel := chwrapper.WriteContext(ctx)
		err := conn.Exec(wctx, captureSQL(from, end))
		cancel()
		if err != nil {
			return fmt.Errorf("window %s..%s: %w", from.Format(time.RFC3339), end.Format(time.RFC3339), err)
		}
		wctx, cancel = chwrapper.WriteContext(ctx)
		err = conn.Exec(wctx, `
			INSERT INTO indexer_watermarks (chain_id, indexer_name, granularity, last_period, last_block_num)
			VALUES (?, ?, 'time', ?, 0)`, uint32(ChainID), watermark, end)
		cancel()
		if err != nil {
			return err
		}
		if end.Sub(from) > time.Hour {
			slog.Info("ICM messages synced", "from", from.Format("2006-01-02"), "to", end.Format("2006-01-02"), "duration", time.Since(start))
		}
		from = end
	}
	return nil
}

// Through is the time up to which ICM messages are complete, zero before the
// first window.
func Through(ctx context.Context, conn driver.Conn) (time.Time, error) {
	var t time.Time
	rows, err := conn.Query(ctx, `
		SELECT last_period FROM indexer_watermarks FINAL
		WHERE chain_id = ? AND indexer_name = ?`, uint32(ChainID), watermark)
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

// captureSQL copies the window's sends and receives. topic1 is the messageID
// and topic2 the other chain's blockchain ID in both events.
func captureSQL(from, to time.Time) string {
	return fmt.Sprintf(`
		INSERT INTO icm_messages (direction, message_id, counterparty, block_time, block_number,
			transaction_hash, log_index)
		SELECT if(topic0 = unhex('%s'), 'sent', 'received'), assumeNotNull(topic1), assumeNotNull(topic2),
			block_time, block_number, transaction_hash, log_index
		FROM raw_logs
		WHERE chain_id = %d
		  AND block_time >= %s AND block_time < %s
		  AND address = unhex('%s')
		  AND topic0 IN (unhex('%s'), unhex('%s'))
		  AND topic1 IS NOT NULL AND topic2 IS NOT NULL
	`, topicSend, ChainID, timeLit(from), timeLit(to), Messenger, topicSend, topicReceive)
}

func timeLit(t time.Time) string {
	return fmt.Sprintf("toDateTime64('%s', 3, 'UTC')", t.UTC().Format("2006-01-02 15:04:05.000"))
}
