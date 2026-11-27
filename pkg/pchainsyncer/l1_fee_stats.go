package pchainsyncer

import (
	"context"
	"fmt"
	"time"

	"icicle/pkg/chwrapper"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// L1FeeStats represents fee statistics for an L1 subnet
type L1FeeStats struct {
	SubnetID        string
	TotalDeposited  uint64
	InitialDeposits uint64
	TopUpDeposits   uint64
	TotalRefunded   uint64
	CurrentBalance  uint64
	TotalFeesPaid   uint64
	DepositTxCount  uint32
	ValidatorCount  uint32
	PChainID        uint32
}

// CalculateL1FeeStats calculates fee statistics for all L1 subnets
func CalculateL1FeeStats(ctx context.Context, conn clickhouse.Conn, pchainID uint32) ([]L1FeeStats, error) {
	ctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	// This query calculates fee stats using deposits, refunds, and current balances
	query := `
		WITH
		-- All deposits from l1_validator_balance_txs (deduplicated)
		deposits AS (
			SELECT
				subnet_id,
				sumIf(amount, tx_type IN ('ConvertSubnetToL1', 'RegisterL1Validator')) as initial_deposits,
				sumIf(amount, tx_type = 'IncreaseL1ValidatorBalance') as topup_deposits,
				sumIf(amount, tx_type IN ('ConvertSubnetToL1', 'RegisterL1Validator', 'IncreaseL1ValidatorBalance')) as total_deposits,
				countIf(tx_type IN ('ConvertSubnetToL1', 'RegisterL1Validator')) as validator_count,
				countIf(tx_type IN ('ConvertSubnetToL1', 'RegisterL1Validator', 'IncreaseL1ValidatorBalance')) as tx_count
			FROM l1_validator_balance_txs FINAL
			WHERE p_chain_id = ?
			GROUP BY subnet_id
		),
		-- Total refunds from l1_validator_refunds
		refunds AS (
			SELECT
				subnet_id,
				sum(refund_amount) as total_refunds
			FROM l1_validator_refunds FINAL
			WHERE p_chain_id = ?
			GROUP BY subnet_id
		),
		-- Current balances from l1_validator_state
		current_balances AS (
			SELECT
				subnet_id,
				sum(balance) as current_balance
			FROM l1_validator_state FINAL
			WHERE p_chain_id = ?
			GROUP BY subnet_id
		),
		-- All L1 subnets
		l1_subnets AS (
			SELECT DISTINCT subnet_id
			FROM subnets FINAL
			WHERE p_chain_id = ? AND subnet_type = 'l1'
		)
		SELECT
			l1.subnet_id,
			COALESCE(d.initial_deposits, 0) as initial_deposits,
			COALESCE(d.topup_deposits, 0) as topup_deposits,
			COALESCE(d.total_deposits, 0) as total_deposited,
			COALESCE(r.total_refunds, 0) as total_refunded,
			COALESCE(cb.current_balance, 0) as current_balance,
			COALESCE(d.tx_count, 0) as deposit_tx_count,
			COALESCE(d.validator_count, 0) as validator_count
		FROM l1_subnets l1
		LEFT JOIN deposits d ON l1.subnet_id = d.subnet_id
		LEFT JOIN refunds r ON l1.subnet_id = r.subnet_id
		LEFT JOIN current_balances cb ON l1.subnet_id = cb.subnet_id
		ORDER BY total_deposited DESC
	`

	rows, err := conn.Query(ctx, query, pchainID, pchainID, pchainID, pchainID)
	if err != nil {
		return nil, fmt.Errorf("failed to query fee stats: %w", err)
	}
	defer rows.Close()

	var stats []L1FeeStats
	for rows.Next() {
		var s L1FeeStats
		var initialDeposits, topupDeposits, totalDeposited, totalRefunded, currentBalance uint64
		var depositTxCount, validatorCount uint64

		if err := rows.Scan(
			&s.SubnetID,
			&initialDeposits,
			&topupDeposits,
			&totalDeposited,
			&totalRefunded,
			&currentBalance,
			&depositTxCount,
			&validatorCount,
		); err != nil {
			return nil, fmt.Errorf("failed to scan fee stats row: %w", err)
		}

		s.InitialDeposits = initialDeposits
		s.TopUpDeposits = topupDeposits
		s.TotalDeposited = totalDeposited
		s.TotalRefunded = totalRefunded
		s.CurrentBalance = currentBalance
		s.DepositTxCount = uint32(depositTxCount)
		s.ValidatorCount = uint32(validatorCount)
		s.PChainID = pchainID

		// Calculate fees paid: deposited - refunded - current balance
		// This gives accurate fees for both active and disabled validators
		if totalDeposited > totalRefunded+currentBalance {
			s.TotalFeesPaid = totalDeposited - totalRefunded - currentBalance
		} else {
			s.TotalFeesPaid = 0
		}

		stats = append(stats, s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fee stats rows error: %w", err)
	}

	return stats, nil
}

// InsertL1FeeStats inserts or updates L1 fee statistics
func InsertL1FeeStats(ctx context.Context, conn clickhouse.Conn, stats []L1FeeStats) error {
	ctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	if len(stats) == 0 {
		return nil
	}

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO l1_fee_stats (
		subnet_id, total_deposited, initial_deposits, top_up_deposits, total_refunded,
		current_balance, total_fees_paid, deposit_tx_count, validator_count,
		p_chain_id, last_updated
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare batch: %w", err)
	}
	defer func() { _ = batch.Abort() }()

	now := time.Now()
	for _, s := range stats {
		err = batch.Append(
			s.SubnetID,
			s.TotalDeposited,
			s.InitialDeposits,
			s.TopUpDeposits,
			s.TotalRefunded,
			s.CurrentBalance,
			s.TotalFeesPaid,
			s.DepositTxCount,
			s.ValidatorCount,
			s.PChainID,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to append fee stats for %s: %w", s.SubnetID, err)
		}
	}

	return chwrapper.RetryableBatchSend(batch)
}
