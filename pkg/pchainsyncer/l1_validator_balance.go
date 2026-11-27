package pchainsyncer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"icicle/pkg/chwrapper"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// L1ValidatorBalanceTx represents a balance-affecting transaction for a validator
type L1ValidatorBalanceTx struct {
	ValidationID string
	TxID         string
	TxType       string
	BlockNumber  uint64
	BlockTime    time.Time
	Amount       uint64
	SubnetID     string
	NodeID       string
	PChainID     uint32
}

// SyncL1ValidatorBalanceTxs syncs all balance-affecting transactions to the l1_validator_balance_txs table
// This includes: ConvertSubnetToL1, RegisterL1Validator, and IncreaseL1ValidatorBalance transactions
func SyncL1ValidatorBalanceTxs(ctx context.Context, conn clickhouse.Conn, pchainID uint32) error {
	ctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	// Check last synced block
	var lastSyncedBlock uint64
	err := conn.QueryRow(ctx, `
		SELECT COALESCE(max(block_number), 0) FROM l1_validator_balance_txs WHERE p_chain_id = ?
	`, pchainID).Scan(&lastSyncedBlock)
	if err != nil {
		slog.Warn("Could not get last synced block for balance txs", "error", err)
		lastSyncedBlock = 0
	}

	var txs []L1ValidatorBalanceTx

	// 1. Sync IncreaseL1ValidatorBalance transactions (top-ups)
	topUpQuery := `
		SELECT
			toString(t.tx_data.validationID) as validation_id,
			t.tx_id,
			'IncreaseL1ValidatorBalance' as tx_type,
			t.block_number,
			t.block_time,
			toUInt64OrZero(toString(t.tx_data.balance)) as amount,
			vs.subnet_id,
			vs.node_id
		FROM p_chain_txs t
		JOIN l1_validator_state vs FINAL ON toString(t.tx_data.validationID) = vs.validation_id AND vs.p_chain_id = t.p_chain_id
		WHERE t.p_chain_id = ?
		  AND t.tx_type = 'IncreaseL1ValidatorBalance'
		  AND t.block_number > ?
		ORDER BY t.block_number ASC
	`

	topUpRows, err := conn.Query(ctx, topUpQuery, pchainID, lastSyncedBlock)
	if err != nil {
		return fmt.Errorf("failed to query IncreaseL1ValidatorBalance txs: %w", err)
	}

	for topUpRows.Next() {
		var tx L1ValidatorBalanceTx
		if err := topUpRows.Scan(
			&tx.ValidationID, &tx.TxID, &tx.TxType, &tx.BlockNumber,
			&tx.BlockTime, &tx.Amount, &tx.SubnetID, &tx.NodeID,
		); err != nil {
			slog.Warn("Failed to scan top-up tx", "error", err)
			continue
		}
		tx.PChainID = pchainID
		txs = append(txs, tx)
	}
	topUpRows.Close()

	// 2. Sync initial deposits from l1_validator_history
	// This covers both ConvertSubnetToL1 and RegisterL1Validator transactions
	// On first sync (lastSyncedBlock == 0), sync all; otherwise only new blocks
	initialQuery := `
		SELECT
			vh.validation_id,
			vh.created_tx_id as tx_id,
			vh.created_tx_type as tx_type,
			vh.created_block as block_number,
			vh.created_time as block_time,
			vh.initial_balance as amount,
			vh.subnet_id,
			vh.node_id
		FROM l1_validator_history vh FINAL
		WHERE vh.p_chain_id = ?
		  AND vh.initial_balance > 0
		  AND vh.created_block > ?
	`

	initialRows, err := conn.Query(ctx, initialQuery, pchainID, lastSyncedBlock)
	if err != nil {
		return fmt.Errorf("failed to query initial deposits: %w", err)
	}

	for initialRows.Next() {
		var tx L1ValidatorBalanceTx
		if err := initialRows.Scan(
			&tx.ValidationID, &tx.TxID, &tx.TxType, &tx.BlockNumber,
			&tx.BlockTime, &tx.Amount, &tx.SubnetID, &tx.NodeID,
		); err != nil {
			slog.Warn("Failed to scan initial deposit", "error", err)
			continue
		}
		tx.PChainID = pchainID
		txs = append(txs, tx)
	}
	initialRows.Close()

	// Insert all transactions
	if len(txs) > 0 {
		slog.Info("Syncing balance transactions", "count", len(txs), "table", "l1_validator_balance_txs")
		if err := InsertL1ValidatorBalanceTxs(ctx, conn, txs); err != nil {
			return fmt.Errorf("failed to insert balance txs: %w", err)
		}
	}

	return nil
}

// InsertL1ValidatorBalanceTxs inserts balance transactions into the table
func InsertL1ValidatorBalanceTxs(ctx context.Context, conn clickhouse.Conn, txs []L1ValidatorBalanceTx) error {
	ctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	if len(txs) == 0 {
		return nil
	}

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO l1_validator_balance_txs (
		validation_id, tx_id, tx_type, block_number, block_time, amount,
		subnet_id, node_id, p_chain_id
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare batch: %w", err)
	}
	defer func() { _ = batch.Abort() }()

	for _, tx := range txs {
		err = batch.Append(
			tx.ValidationID,
			tx.TxID,
			tx.TxType,
			tx.BlockNumber,
			tx.BlockTime,
			tx.Amount,
			tx.SubnetID,
			tx.NodeID,
			tx.PChainID,
		)
		if err != nil {
			return fmt.Errorf("failed to append tx %s: %w", tx.TxID, err)
		}
	}

	return chwrapper.RetryableBatchSend(batch)
}

// UpdatePerValidatorFeeStats calculates and updates fee statistics for each L1 validator
func UpdatePerValidatorFeeStats(ctx context.Context, conn clickhouse.Conn, pchainID uint32) error {
	ctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	// This query calculates per-validator fee stats and updates l1_validator_state
	// We calculate: initial_deposit, total_topups, refund_amount, fees_paid
	query := `
		WITH
		-- Everything is keyed by validation_id, not node_id: a node that
		-- re-registers gets a new validation, and its old deposits, top-ups
		-- and refunds must not leak into the new one.
		-- Get initial deposits from history
		initial AS (
			SELECT
				validation_id,
				any(initial_balance) as initial_deposit
			FROM l1_validator_history FINAL
			WHERE p_chain_id = ? AND validation_id != ''
			GROUP BY validation_id
		),
		-- Get top-ups per validation
		topups AS (
			SELECT
				validation_id,
				sum(amount) as total_topups
			FROM l1_validator_balance_txs FINAL
			WHERE p_chain_id = ? AND tx_type = 'IncreaseL1ValidatorBalance' AND validation_id != ''
			GROUP BY validation_id
		),
		-- Get refunds per validator (use validation_id to join)
		refunds AS (
			SELECT
				validation_id,
				sum(refund_amount) as refund_amount
			FROM l1_validator_refunds FINAL
			WHERE p_chain_id = ?
			GROUP BY validation_id
		)
		SELECT
			v.subnet_id,
			v.validation_id,
			v.node_id,
			v.balance,
			v.weight,
			v.start_time,
			v.end_time,
			v.uptime_percentage,
			v.active,
			COALESCE(i.initial_deposit, 0) as initial_deposit,
			COALESCE(t.total_topups, 0) as total_topups,
			COALESCE(rf.refund_amount, 0) as refund_amount
		FROM l1_validator_state v FINAL
		LEFT JOIN initial i ON v.validation_id = i.validation_id
		LEFT JOIN topups t ON v.validation_id = t.validation_id
		LEFT JOIN refunds rf ON v.validation_id = rf.validation_id
		WHERE v.p_chain_id = ?
		  AND v.subnet_id != '11111111111111111111111111111111LpoYY'
	`

	rows, err := conn.Query(ctx, query, pchainID, pchainID, pchainID, pchainID)
	if err != nil {
		return fmt.Errorf("failed to query validator fee data: %w", err)
	}
	defer rows.Close()

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO l1_validator_state (
		subnet_id, validation_id, node_id, balance, weight,
		start_time, end_time, uptime_percentage, active, last_updated, p_chain_id,
		initial_deposit, total_topups, refund_amount, fees_paid
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare batch: %w", err)
	}
	defer func() { _ = batch.Abort() }()

	now := time.Now()
	updateCount := 0

	for rows.Next() {
		var subnetID, validationID, nodeID string
		var balance, weight uint64
		var startTime, endTime time.Time
		var uptime float64
		var active bool
		var initialDeposit, totalTopups, refundAmount uint64

		if err := rows.Scan(
			&subnetID, &validationID, &nodeID, &balance, &weight,
			&startTime, &endTime, &uptime, &active,
			&initialDeposit, &totalTopups, &refundAmount,
		); err != nil {
			slog.Warn("Failed to scan validator row", "error", err)
			continue
		}

		// Calculate fees paid: deposited - refunded - current balance
		totalDeposited := initialDeposit + totalTopups
		var feesPaid uint64
		if totalDeposited > (refundAmount + balance) {
			feesPaid = totalDeposited - refundAmount - balance
		}

		err = batch.Append(
			subnetID, validationID, nodeID, balance, weight,
			startTime, endTime, uptime, active, now, pchainID,
			initialDeposit, totalTopups, refundAmount, feesPaid,
		)
		if err != nil {
			slog.Warn("Failed to append validator", "node_id", nodeID, "error", err)
			continue
		}
		updateCount++
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows error: %w", err)
	}

	if updateCount > 0 {
		if err := chwrapper.RetryableBatchSend(batch); err != nil {
			return fmt.Errorf("failed to send batch: %w", err)
		}
		slog.Info("Updated fee stats for validators", "count", updateCount)
	}

	return nil
}
