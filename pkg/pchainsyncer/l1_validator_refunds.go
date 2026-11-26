package pchainsyncer

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"icicle/pkg/chwrapper"
	"icicle/pkg/pchainrpc"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp/message"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp/payload"
)

// parseWarpPayload extracts the inner payload from a Warp message hex string.
// It trims the 0x prefix, hex decodes, skips the 42-byte unsigned warp header,
// reads the payload length, parses the AddressedCall, and returns the inner Payload bytes.
func parseWarpPayload(messageHex string) ([]byte, error) {
	messageHex = strings.TrimPrefix(messageHex, "0x")
	messageBytes, err := hex.DecodeString(messageHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hex: %w", err)
	}

	if len(messageBytes) < 42 { // 2 + 4 + 32 + 4 minimum
		return nil, fmt.Errorf("message too short: %d bytes", len(messageBytes))
	}

	// Skip codec version (2 bytes), network ID (4 bytes), source chain ID (32 bytes)
	payloadLenOffset := 2 + 4 + 32
	if len(messageBytes) < payloadLenOffset+4 {
		return nil, fmt.Errorf("message too short for payload length")
	}

	// Read payload length (big endian uint32)
	payloadLen := uint32(messageBytes[payloadLenOffset])<<24 |
		uint32(messageBytes[payloadLenOffset+1])<<16 |
		uint32(messageBytes[payloadLenOffset+2])<<8 |
		uint32(messageBytes[payloadLenOffset+3])

	payloadStart := payloadLenOffset + 4
	payloadEnd := payloadStart + int(payloadLen)

	if payloadEnd > len(messageBytes) {
		return nil, fmt.Errorf("payload extends beyond message: need %d, have %d", payloadEnd, len(messageBytes))
	}

	warpPayload := messageBytes[payloadStart:payloadEnd]

	addressedCall, err := payload.ParseAddressedCall(warpPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AddressedCall: %w", err)
	}

	return addressedCall.Payload, nil
}

// L1ValidatorRefund represents a refund when a validator exits
type L1ValidatorRefund struct {
	TxID          string
	TxType        string
	ValidationID  string
	SubnetID      string
	RefundAmount  uint64
	RefundAddress string
	BlockNumber   uint64
	BlockTime     time.Time
	PChainID      uint32
}

// SyncL1ValidatorRefunds syncs refund transactions from DisableL1Validator and SetL1ValidatorWeight (weight=0) txs
// It calculates the refund amount based on deposits minus fees
func SyncL1ValidatorRefunds(ctx context.Context, conn clickhouse.Conn, fetcher *pchainrpc.Fetcher, pchainID uint32) error {
	// Query both DisableL1Validator and SetL1ValidatorWeight transactions that haven't been processed yet.
	// We rely on the NOT IN subquery for dedup rather than a block_number filter,
	// because SetL1ValidatorWeight txs may exist at any block in history.
	query := `
		SELECT
			tx_id,
			tx_type,
			CASE WHEN tx_type = 'DisableL1Validator' THEN toString(tx_data.validationID) ELSE '' END as validation_id,
			CASE WHEN tx_type = 'SetL1ValidatorWeight' THEN toString(tx_data.message) ELSE '' END as message,
			block_number,
			block_time
		FROM p_chain_txs
		WHERE p_chain_id = ?
		  AND tx_type IN ('DisableL1Validator', 'SetL1ValidatorWeight')
		  AND tx_id NOT IN (SELECT tx_id FROM l1_validator_refunds WHERE p_chain_id = ?)
		ORDER BY block_number ASC
	`

	// Bounded per-op (not function-level): the per-item RPC loop below can be
	// legitimately slow, so only the DB operations carry deadlines. This is the
	// function whose unbounded INSERT parked a pooled connection forever when a
	// connection died mid-stream (2026-07-17, ATTEMPT_TO_READ_AFTER_EOF on
	// l1_validator_refunds) and froze the validator syncer for 4 days.
	qctx, qcancel := chwrapper.ReadContext(ctx)
	defer qcancel()
	rows, err := conn.Query(qctx, query, pchainID, pchainID)
	if err != nil {
		return fmt.Errorf("failed to query refund txs: %w", err)
	}
	defer rows.Close()

	var refunds []L1ValidatorRefund
	for rows.Next() {
		var txID, txType, validationID, messageHex string
		var blockNumber uint64
		var blockTime time.Time
		if err := rows.Scan(&txID, &txType, &validationID, &messageHex, &blockNumber, &blockTime); err != nil {
			slog.Warn("Failed to scan refund tx", "error", err)
			continue
		}

		r := L1ValidatorRefund{
			TxID:        txID,
			BlockNumber: blockNumber,
			BlockTime:   blockTime,
			PChainID:    pchainID,
		}

		switch txType {
		case "DisableL1Validator":
			r.ValidationID = validationID
			r.TxType = txType
		case "SetL1ValidatorWeight":
			// Parse Warp message to get L1ValidatorWeight
			innerPayload, err := parseWarpPayload(messageHex)
			if err != nil {
				slog.Warn("Failed to parse Warp message for SetL1ValidatorWeight", "tx_id", txID, "error", err)
				continue
			}
			weightMsg, err := message.ParseL1ValidatorWeight(innerPayload)
			if err != nil {
				slog.Warn("Failed to parse L1ValidatorWeight payload", "tx_id", txID, "error", err)
				continue
			}
			// Only weight=0 means validator removal/refund
			if weightMsg.Weight > 0 {
				continue
			}
			r.ValidationID = weightMsg.ValidationID.String()
			r.TxType = txType
		}

		if r.ValidationID == "" {
			continue
		}
		refunds = append(refunds, r)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("refund rows error: %w", err)
	}

	if len(refunds) == 0 {
		return nil
	}

	// For each refund, fetch the actual refund amount from UTXOs via RPC
	for i := range refunds {
		// Get subnet_id and remaining_balance_owner from l1_validator_history (preferred)
		// This works for disabled validators where RPC fails
		var subnetID, refundAddress string
		hctx, hcancel := chwrapper.ReadContext(ctx)
		err := conn.QueryRow(hctx, `
			SELECT subnet_id, remaining_balance_owner FROM l1_validator_history FINAL
			WHERE validation_id = ? AND p_chain_id = ?
		`, refunds[i].ValidationID, pchainID).Scan(&subnetID, &refundAddress)
		hcancel()
		if err != nil || refundAddress == "" {
			// Fall back to l1_validator_state for subnet_id if not found in history
			if subnetID == "" {
				sctx, scancel := chwrapper.ReadContext(ctx)
				err = conn.QueryRow(sctx, `
					SELECT subnet_id FROM l1_validator_state FINAL
					WHERE validation_id = ? AND p_chain_id = ?
				`, refunds[i].ValidationID, pchainID).Scan(&subnetID)
				scancel()
				if err != nil {
					slog.Warn("Could not find subnet for validation_id", "validation_id", refunds[i].ValidationID, "error", err)
					continue
				}
			}

			// Try to get remainingBalanceOwner from RPC if not in history
			if refundAddress == "" {
				validatorInfo, rpcErr := fetcher.GetL1Validator(ctx, refunds[i].ValidationID)
				if rpcErr != nil {
					slog.Warn("Could not get L1 validator info", "validation_id", refunds[i].ValidationID, "error", rpcErr)
					continue
				}

				if len(validatorInfo.RemainingBalanceOwner.Addresses) == 0 {
					slog.Warn("No remainingBalanceOwner address for validator", "validation_id", refunds[i].ValidationID)
					continue
				}
				refundAddress = validatorInfo.RemainingBalanceOwner.Addresses[0]
			}
		}

		refunds[i].SubnetID = subnetID
		refunds[i].RefundAddress = refundAddress

		// Calculate refund from deposits minus fees
		// Formula: refund = deposits - (activeSeconds * 512 nAVAX/sec)
		// This is reliable because fee rate has always been 512 nAVAX/sec (network < 10k validators)
		refundAmount, err := calculateRefundFromDeposits(ctx, conn, refunds[i].ValidationID, pchainID, refunds[i].BlockTime)
		if err != nil {
			slog.Warn("Could not calculate refund", "validation_id", refunds[i].ValidationID, "error", err)
			continue
		}
		refunds[i].RefundAmount = refundAmount
	}

	// Insert refunds. The write deadline is what lets a severed connection fail
	// fast instead of blocking batch.Send forever (the 2026-07-17 park).
	wctx, wcancel := chwrapper.WriteContext(ctx)
	defer wcancel()
	batch, err := conn.PrepareBatch(wctx, `INSERT INTO l1_validator_refunds (
		tx_id, validation_id, subnet_id, refund_amount, refund_address, block_number, block_time, p_chain_id
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare batch: %w", err)
	}
	defer func() { _ = batch.Abort() }()

	insertCount := 0
	for _, r := range refunds {
		if r.SubnetID == "" || r.RefundAmount == 0 {
			continue // Skip if we couldn't find the subnet or refund amount
		}
		err = batch.Append(
			r.TxID,
			r.ValidationID,
			r.SubnetID,
			r.RefundAmount,
			r.RefundAddress,
			r.BlockNumber,
			r.BlockTime,
			r.PChainID,
		)
		if err != nil {
			return fmt.Errorf("failed to append refund %s: %w", r.TxID, err)
		}
		insertCount++
	}

	if insertCount > 0 {
		if err := chwrapper.RetryableBatchSend(batch); err != nil {
			return fmt.Errorf("failed to send refunds batch: %w", err)
		}
		slog.Info("Synced validator refunds", "count", insertCount)

		// Also insert refunds into l1_validator_balance_txs so they appear in balance history
		var balanceTxs []L1ValidatorBalanceTx
		for _, r := range refunds {
			if r.SubnetID == "" || r.RefundAmount == 0 {
				continue
			}
			// Look up node_id for this validator
			var nodeID string
			nctx, ncancel := chwrapper.ReadContext(ctx)
			_ = conn.QueryRow(nctx, `
				SELECT node_id FROM l1_validator_state FINAL
				WHERE validation_id = ? AND p_chain_id = ?
			`, r.ValidationID, pchainID).Scan(&nodeID)
			ncancel()

			balanceTxs = append(balanceTxs, L1ValidatorBalanceTx{
				ValidationID: r.ValidationID,
				TxID:         r.TxID,
				TxType:       r.TxType,
				BlockNumber:  r.BlockNumber,
				BlockTime:    r.BlockTime,
				Amount:       r.RefundAmount,
				SubnetID:     r.SubnetID,
				NodeID:       nodeID,
				PChainID:     r.PChainID,
			})
		}
		if len(balanceTxs) > 0 {
			if err := InsertL1ValidatorBalanceTxs(ctx, conn, balanceTxs); err != nil {
				slog.Warn("Failed to insert refund balance txs", "error", err)
			}
		}
	}

	return nil
}

// SyncL1ValidatorWeightTxs decodes every SetL1ValidatorWeight transaction's signed
// Warp message (an L1ValidatorWeightMessage carried in tx_data.message) and persists
// the extracted validationID, nonce, and new weight into l1_validator_weight_txs.
// The validationID isn't a top-level JSON field, so this decode is the only way to
// resolve these txs to a validator (and thus a subnet). Incremental: txs already
// mapped are skipped, so the first run backfills all history.
func SyncL1ValidatorWeightTxs(ctx context.Context, conn clickhouse.Conn, pchainID uint32) error {
	ctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	rows, err := conn.Query(ctx, `
		SELECT tx_id, toString(tx_data.message) AS message, block_number, block_time
		FROM p_chain_txs
		WHERE p_chain_id = ?
		  AND tx_type = 'SetL1ValidatorWeight'
		  AND tx_id NOT IN (SELECT tx_id FROM l1_validator_weight_txs WHERE p_chain_id = ?)
		ORDER BY block_number ASC
	`, pchainID, pchainID)
	if err != nil {
		return fmt.Errorf("failed to query SetL1ValidatorWeight txs: %w", err)
	}
	defer rows.Close()

	type weightTx struct {
		txID         string
		validationID string
		nonce        uint64
		weight       uint64
		blockNumber  uint64
		blockTime    time.Time
	}

	var wtxs []weightTx
	for rows.Next() {
		var txID, messageHex string
		var blockNumber uint64
		var blockTime time.Time
		if err := rows.Scan(&txID, &messageHex, &blockNumber, &blockTime); err != nil {
			slog.Warn("Failed to scan SetL1ValidatorWeight row", "error", err)
			continue
		}
		innerPayload, err := parseWarpPayload(messageHex)
		if err != nil {
			slog.Warn("Failed to parse Warp message for SetL1ValidatorWeight", "tx_id", txID, "error", err)
			continue
		}
		weightMsg, err := message.ParseL1ValidatorWeight(innerPayload)
		if err != nil {
			slog.Warn("Failed to parse L1ValidatorWeight payload", "tx_id", txID, "error", err)
			continue
		}
		wtxs = append(wtxs, weightTx{
			txID:         txID,
			validationID: weightMsg.ValidationID.String(),
			nonce:        weightMsg.Nonce,
			weight:       weightMsg.Weight,
			blockNumber:  blockNumber,
			blockTime:    blockTime,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("SetL1ValidatorWeight rows error: %w", err)
	}

	if len(wtxs) == 0 {
		return nil
	}

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO l1_validator_weight_txs (
		tx_id, validation_id, nonce, weight, block_number, block_time, p_chain_id
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare weight-txs batch: %w", err)
	}
	defer func() { _ = batch.Abort() }()
	for _, w := range wtxs {
		if err := batch.Append(w.txID, w.validationID, w.nonce, w.weight, w.blockNumber, w.blockTime, pchainID); err != nil {
			return fmt.Errorf("failed to append weight tx %s: %w", w.txID, err)
		}
	}
	if err := chwrapper.RetryableBatchSend(batch); err != nil {
		return fmt.Errorf("failed to send weight-txs batch: %w", err)
	}
	slog.Info("Synced SetL1ValidatorWeight validations", "count", len(wtxs))
	return nil
}

// calculateRefundFromDeposits calculates refund when UTXO has been spent
// Uses: refund = total_deposits - fees_paid (based on time active and min fee rate)
// disableTime is the block_time from the DisableL1Validator transaction
func calculateRefundFromDeposits(ctx context.Context, conn clickhouse.Conn, validationID string, pchainID uint32, disableTime time.Time) (uint64, error) {
	ctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	// Find the start time for this validation period
	// If this validator was disabled before, the start time is the previous disable time
	// (when it was re-activated by depositing more funds)
	// Otherwise, use the original creation time
	var startTime time.Time
	var previousDisableTime time.Time

	// Check if there was a previous removal for this validator (before current disableTime)
	// Query l1_validator_refunds which covers both DisableL1Validator and SetL1ValidatorWeight removals
	err := conn.QueryRow(ctx, `
		SELECT MAX(block_time)
		FROM l1_validator_refunds FINAL
		WHERE p_chain_id = ?
		  AND block_time < ?
		  AND validation_id = ?
	`, pchainID, disableTime, validationID).Scan(&previousDisableTime)

	// ClickHouse returns epoch time (1970-01-01) when MAX() finds no rows
	// We need to check if this is a real previous disable or just the epoch default
	epochTime := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	hasPreviousDisable := err == nil && !previousDisableTime.IsZero() && previousDisableTime.After(epochTime)

	if hasPreviousDisable {
		// This is a subsequent disable - start time is when the validator was re-activated
		// (right after the previous disable)
		startTime = previousDisableTime
	} else {
		// First disable - use original creation time
		err = conn.QueryRow(ctx, `
			SELECT created_time
			FROM l1_validator_history FINAL
			WHERE validation_id = ? AND p_chain_id = ?
		`, validationID, pchainID).Scan(&startTime)
		if err != nil {
			// Fallback to l1_validator_state
			err = conn.QueryRow(ctx, `
				SELECT start_time
				FROM l1_validator_state FINAL
				WHERE validation_id = ? AND p_chain_id = ?
			`, validationID, pchainID).Scan(&startTime)
			if err != nil {
				return 0, fmt.Errorf("failed to get validator start time: %w", err)
			}
		}
	}

	// Get total deposits for this validation period only
	// For first period: include deposits from creation time (>=) through disable time
	// For subsequent periods: include deposits after previous disable (>) through current disable
	var totalDeposits uint64
	if !hasPreviousDisable {
		// First period - include the initial deposit at creation time
		err = conn.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount), 0)
			FROM l1_validator_balance_txs FINAL
			WHERE validation_id = ? AND p_chain_id = ?
			  AND tx_type IN ('ConvertSubnetToL1', 'RegisterL1Validator', 'IncreaseL1ValidatorBalance')
			  AND block_time >= ? AND block_time <= ?
		`, validationID, pchainID, startTime, disableTime).Scan(&totalDeposits)
	} else {
		// Subsequent period - exclude deposits from before the previous disable
		err = conn.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount), 0)
			FROM l1_validator_balance_txs FINAL
			WHERE validation_id = ? AND p_chain_id = ?
			  AND tx_type IN ('ConvertSubnetToL1', 'RegisterL1Validator', 'IncreaseL1ValidatorBalance')
			  AND block_time > ? AND block_time <= ?
		`, validationID, pchainID, startTime, disableTime).Scan(&totalDeposits)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get deposits: %w", err)
	}

	// Calculate fees using minimum rate (512 nAVAX/second)
	// This is an approximation - actual rate may be higher if network had >10k validators
	const minFeeRate = 512 // nAVAX per second
	activeSeconds := uint64(disableTime.Sub(startTime).Seconds())
	feesConsumed := activeSeconds * minFeeRate

	if feesConsumed >= totalDeposits {
		return 0, nil // All deposits consumed by fees
	}

	refund := totalDeposits - feesConsumed
	slog.Info("Calculated refund", "validation_id", validationID, "deposits", totalDeposits, "start", startTime.Format("2006-01-02 15:04:05"), "active_seconds", activeSeconds, "fees", feesConsumed, "refund_navax", refund, "refund_avax", fmt.Sprintf("%.6f", float64(refund)/1e9))

	return refund, nil
}
