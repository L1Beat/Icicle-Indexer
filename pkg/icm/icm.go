// Package icm decodes C-Chain ICM (Teleporter) messages out of raw_logs: one row
// per message sent from the C-Chain and per message received on it, keyed by
// messageID within each direction, with the C-Chain and L1 contracts, fees,
// relayer and, for ICTT token transfers, the recipient and amount. Only the
// official TeleporterMessenger is read, since any contract can emit an event
// with the same signature. Messages between two L1s that never touch the
// C-Chain are not seen here.
package icm

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
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
	// MessageExecuted(bytes32 indexed messageID, bytes32 indexed
	// sourceBlockchainID): a received message ran, on receipt or on a retry.
	// A receive whose execution failed moved no tokens.
	topicExecuted = "34795cc6b122b9a0ae684946319f1e14a577b4e8f9b3dda9ac94c21a54d3188c"
)

// Start is the UTC day the TeleporterMessenger was deployed on the C-Chain
// (block 42526120, 2024-03-05 22:13 UTC).
var Start = time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC)

// windowDays bounds one raw_logs scan. Rows are streamed, so a busy window
// (the Omnicoin burst put 258k messages in one week) only costs time.
const windowDays = 7

// insertBatch bounds the rows sent in one INSERT.
const insertBatch = 20000

// budget bounds the work per Sync call, so the history fill does not hold up
// the C-Chain snapshot cycle for long.
const budget = 2 * time.Minute

// watermark names the indexer_watermarks row holding the time up to which
// raw_logs have been copied (last_period, exclusive).
const watermark = "icm/through"

// rescans re-read history the main fill passed before a decoder change. Each
// has its own watermark under watermark/rescan/<name>, and covers up to where
// the main fill is.
var rescans = []string{
	// Contracts, fees, relayers and ICTT payloads, added after the counts.
	"details",
	// TokenHome remote settings, to scale ICTT amounts to C-Chain units.
	"remotes",
	// MessageExecuted, and ICTT call payloads read one word in.
	"executed",
}

// Sync decodes messages from the watermark up to the C-Chain sync watermark,
// a window at a time, within budget, after any pending rescan. caller reads
// the C-Chain (eth_call) to check ICTT contracts.
func Sync(ctx context.Context, conn driver.Conn, caller Caller) error {
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
	for _, name := range rescans {
		mark := watermark + "/rescan/" + name
		done, err := readWatermark(ctx, conn, mark)
		if err != nil {
			return err
		}
		if done.IsZero() {
			done = Start
		}
		if err := syncRange(ctx, conn, caller, done, from, deadline, mark, "ICM rescan "+name); err != nil {
			return err
		}
	}
	return syncRange(ctx, conn, caller, from, synced, deadline, watermark, "ICM messages synced")
}

// syncRange runs windows from from to to until done or past deadline, moving
// the named watermark after each.
func syncRange(ctx context.Context, conn driver.Conn, caller Caller, from, to, deadline time.Time, mark, msg string) error {
	for from.Before(to) && time.Now().Before(deadline) {
		end := from.AddDate(0, 0, windowDays)
		if end.After(to) {
			end = to
		}
		start := time.Now()
		n, err := capture(ctx, conn, from, end)
		if err != nil {
			return fmt.Errorf("window %s..%s: %w", from.Format(time.RFC3339), end.Format(time.RFC3339), err)
		}
		wctx, cancel := chwrapper.WriteContext(ctx)
		err = conn.Exec(wctx, executedSQL(from, end))
		cancel()
		if err != nil {
			return fmt.Errorf("executions %s..%s: %w", from.Format(time.RFC3339), end.Format(time.RFC3339), err)
		}
		if err := discover(ctx, conn, caller, from, end); err != nil {
			return fmt.Errorf("apps %s..%s: %w", from.Format(time.RFC3339), end.Format(time.RFC3339), err)
		}
		if err := discoverRemotes(ctx, conn, caller, from, end); err != nil {
			return fmt.Errorf("remotes %s..%s: %w", from.Format(time.RFC3339), end.Format(time.RFC3339), err)
		}
		wctx, cancel = chwrapper.WriteContext(ctx)
		err = conn.Exec(wctx, `
			INSERT INTO indexer_watermarks (chain_id, indexer_name, granularity, last_period, last_block_num)
			VALUES (?, ?, 'time', ?, 0)`, uint32(ChainID), mark, end)
		cancel()
		if err != nil {
			return err
		}
		if end.Sub(from) > time.Hour {
			slog.Info(msg, "from", from.Format("2006-01-02"), "to", end.Format("2006-01-02"), "messages", n, "duration", time.Since(start))
		}
		from = end
	}
	return nil
}

// Through is the time up to which ICM messages are complete, zero before the
// first window.
func Through(ctx context.Context, conn driver.Conn) (time.Time, error) {
	return readWatermark(ctx, conn, watermark)
}

func readWatermark(ctx context.Context, conn driver.Conn, name string) (time.Time, error) {
	var t time.Time
	rows, err := conn.Query(ctx, `
		SELECT last_period FROM indexer_watermarks FINAL
		WHERE chain_id = ? AND indexer_name = ?`, uint32(ChainID), name)
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

// capture decodes the window's sends and receives into icm_messages. topic1 is
// the messageID and topic2 the other chain's blockchain ID in both events,
// topic3 a receive's deliverer. A log that does not decode keeps its count
// with empty details.
func capture(ctx context.Context, conn driver.Conn, from, to time.Time) (int, error) {
	rows, err := conn.Query(ctx, fmt.Sprintf(`
		SELECT topic0 = unhex('%s'), assumeNotNull(topic1), assumeNotNull(topic2), ifNull(topic3, ''),
			data, block_time, block_number, transaction_hash, log_index
		FROM raw_logs
		WHERE chain_id = %d
		  AND block_time >= %s AND block_time < %s
		  AND address = unhex('%s')
		  AND topic0 IN (unhex('%s'), unhex('%s'))
		  AND topic1 IS NOT NULL AND topic2 IS NOT NULL
	`, topicSend, ChainID, timeLit(from), timeLit(to), Messenger, topicSend, topicReceive))
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	total, pending := 0, 0
	var batch driver.Batch
	var cancel context.CancelFunc
	send := func() error {
		if batch == nil {
			return nil
		}
		err := batch.Send()
		cancel()
		batch, pending = nil, 0
		return err
	}
	defer func() {
		if batch != nil {
			_ = batch.Abort()
			cancel()
		}
	}()
	for rows.Next() {
		var sent bool
		var id, counterparty, topic3, data, tx string
		var blockTime time.Time
		var block, logIndex uint32
		if err := rows.Scan(&sent, &id, &counterparty, &topic3, &data, &blockTime, &block, &tx, &logIndex); err != nil {
			return total, err
		}
		m, err := decodeMessage(sent, []byte(data), []byte(topic3))
		if err != nil {
			slog.Warn("ICM message did not decode", "tx", fmt.Sprintf("0x%x", tx), "log_index", logIndex, "error", err)
			m = Message{ICTTType: ICTTNone, FeeAmount: new(big.Int), ICTTAmount: new(big.Int)}
		}
		if batch == nil {
			var wctx context.Context
			wctx, cancel = chwrapper.WriteContext(ctx)
			if batch, err = conn.PrepareBatch(wctx, `INSERT INTO icm_messages (direction, message_id, counterparty,
				block_time, block_number, transaction_hash, log_index, local_address, remote_address,
				required_gas_limit, fee_token, fee_amount, deliverer, reward_redeemer,
				ictt_type, ictt_recipient, ictt_amount)`); err != nil {
				cancel()
				batch = nil
				return total, err
			}
		}
		direction := "received"
		if sent {
			direction = "sent"
		}
		if err := batch.Append(direction, id, counterparty, blockTime, block, tx, logIndex,
			string(m.Local[:]), string(m.Remote[:]), m.RequiredGasLimit, string(m.FeeToken[:]), m.FeeAmount,
			string(m.Deliverer[:]), string(m.RewardRedeemer[:]), m.ICTTType, string(m.ICTTRecipient[:]), m.ICTTAmount); err != nil {
			return total, err
		}
		total++
		if pending++; pending >= insertBatch {
			if err := send(); err != nil {
				return total, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return total, err
	}
	return total, send()
}

// executedSQL copies the window's MessageExecuted logs into icm_executions.
func executedSQL(from, to time.Time) string {
	return fmt.Sprintf(`
		INSERT INTO icm_executions (message_id, source, block_time, transaction_hash)
		SELECT assumeNotNull(topic1), assumeNotNull(topic2), block_time, transaction_hash
		FROM raw_logs
		WHERE chain_id = %d
		  AND block_time >= %s AND block_time < %s
		  AND address = unhex('%s') AND topic0 = unhex('%s')
		  AND topic1 IS NOT NULL AND topic2 IS NOT NULL
	`, ChainID, timeLit(from), timeLit(to), Messenger, topicExecuted)
}

func timeLit(t time.Time) string {
	return fmt.Sprintf("toDateTime64('%s', 3, 'UTC')", t.UTC().Format("2006-01-02 15:04:05.000"))
}
