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
	"github.com/ava-labs/avalanchego/ids"
)

// convertClickHouseArrayToJSON converts ClickHouse array string format to JSON array
// ClickHouse toString() on JSON arrays produces: ['{"key":"value"}', '{"key":"value"}']
// We need to convert this to: [{"key":"value"}, {"key":"value"}]
func convertClickHouseArrayToJSON(chArray string) string {
	// Handle empty arrays
	if chArray == "[]" || chArray == "" {
		return "[]"
	}

	// Remove the outer brackets if present
	chArray = strings.TrimSpace(chArray)
	if !strings.HasPrefix(chArray, "[") || !strings.HasSuffix(chArray, "]") {
		return "[]"
	}

	// Remove outer brackets
	inner := chArray[1 : len(chArray)-1]
	if inner == "" {
		return "[]"
	}

	// ClickHouse wraps each JSON object in single quotes: '{"json":...}'
	// We need to find these patterns and extract the JSON objects
	var result strings.Builder
	result.WriteString("[")

	// Split by "','" which separates elements in ClickHouse array notation
	// But we need to be careful because JSON can contain commas
	// Each element is wrapped in single quotes: '{"..."}', '{"..."}'
	inQuote := false
	braceDepth := 0
	elementStart := -1
	first := true

	for i := 0; i < len(inner); i++ {
		c := inner[i]

		if c == '\'' && (i == 0 || inner[i-1] != '\\') {
			if !inQuote {
				// Starting a new element
				inQuote = true
				elementStart = i + 1
			} else {
				// Ending an element
				if elementStart >= 0 && braceDepth == 0 {
					element := inner[elementStart:i]
					if len(element) > 0 {
						if !first {
							result.WriteString(",")
						}
						result.WriteString(element)
						first = false
					}
				}
				inQuote = false
				elementStart = -1
			}
		} else if inQuote {
			if c == '{' {
				braceDepth++
			} else if c == '}' {
				braceDepth--
			}
		}
	}

	result.WriteString("]")
	return result.String()
}

// hexToNodeID converts a hex-encoded node ID (with or without 0x prefix) to CB58 NodeID format
func hexToNodeID(hexStr string) (string, error) {
	// Remove 0x prefix if present
	hexStr = strings.TrimPrefix(hexStr, "0x")

	// Decode hex to bytes
	nodeBytes, err := hex.DecodeString(hexStr)
	if err != nil {
		return "", fmt.Errorf("failed to decode hex: %w", err)
	}

	// NodeID is 20 bytes
	if len(nodeBytes) != 20 {
		return "", fmt.Errorf("invalid node ID length: got %d, expected 20", len(nodeBytes))
	}

	// Create NodeID from bytes
	var nodeID ids.NodeID
	copy(nodeID[:], nodeBytes)

	return nodeID.String(), nil
}

// MaxTxsPerInsertBatch limits the number of transactions per ClickHouse insert
// to avoid memory limit errors on servers with limited RAM
const MaxTxsPerInsertBatch = 5000

// InsertPChainTxs inserts P-chain transaction data into the p_chain_txs table
// It automatically splits large batches to avoid ClickHouse memory limits
func InsertPChainTxs(ctx context.Context, conn clickhouse.Conn, pchainID uint32, blocks []*pchainrpc.JSONBlock) error {
	if len(blocks) == 0 {
		return nil
	}

	// Collect all transactions first
	type txData struct {
		txID        string
		txType      string
		blockHeight uint64
		blockTime   time.Time
		txDataJSON  string
	}
	var allTxs []txData

	for _, block := range blocks {
		for _, tx := range block.Transactions {
			allTxs = append(allTxs, txData{
				txID:        tx.TxID.String(),
				txType:      tx.TxType,
				blockHeight: tx.BlockHeight,
				blockTime:   tx.BlockTime,
				txDataJSON:  string(tx.TxData),
			})
		}
	}

	// Insert in smaller batches to avoid memory issues
	for i := 0; i < len(allTxs); i += MaxTxsPerInsertBatch {
		end := i + MaxTxsPerInsertBatch
		if end > len(allTxs) {
			end = len(allTxs)
		}
		chunk := allTxs[i:end]

		batch, err := conn.PrepareBatch(ctx, `INSERT INTO p_chain_txs (
			tx_id, tx_type, block_number, block_time, p_chain_id, tx_data
		)`)
		if err != nil {
			return fmt.Errorf("failed to prepare batch: %w", err)
		}
		defer func() { _ = batch.Abort() }()

		for _, tx := range chunk {
			err = batch.Append(
				tx.txID,
				tx.txType,
				tx.blockHeight,
				tx.blockTime,
				pchainID,
				tx.txDataJSON,
			)
			if err != nil {
				return fmt.Errorf("failed to append tx %s: %w", tx.txID, err)
			}
		}

		if err := chwrapper.RetryableBatchSend(batch); err != nil {
			return fmt.Errorf("failed to send batch: %w", err)
		}
	}

	return nil
}

// L1Subnet represents an L1 subnet to be tracked
type L1Subnet struct {
	SubnetID                ids.ID
	ChainID                 ids.ID
	ConversionBlock         uint64
	ConversionTime          time.Time
	PChainID                uint32
	ValidatorManagerAddress string // 0x-prefixed EVM address of the ValidatorManager contract
}

// InsertL1Subnets inserts or updates L1 subnet records
func InsertL1Subnets(ctx context.Context, conn clickhouse.Conn, subnets []L1Subnet) error {
	ctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	if len(subnets) == 0 {
		return nil
	}

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO l1_subnets (
		subnet_id, chain_id, conversion_block, conversion_time, validator_manager_address, p_chain_id, last_synced
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare batch: %w", err)
	}
	defer func() { _ = batch.Abort() }()

	now := time.Now()
	for _, subnet := range subnets {
		err = batch.Append(
			subnet.SubnetID.String(), // Store as CB58 string
			subnet.ChainID.String(),  // Store as CB58 string
			subnet.ConversionBlock,
			subnet.ConversionTime,
			subnet.ValidatorManagerAddress,
			subnet.PChainID,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to append subnet %s: %w", subnet.SubnetID, err)
		}
	}

	return chwrapper.RetryableBatchSend(batch)
}

// InsertValidatorStates inserts or updates validator state records
func InsertValidatorStates(ctx context.Context, conn clickhouse.Conn, pchainID uint32, states []*pchainrpc.ValidatorState) error {
	ctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	if len(states) == 0 {
		return nil
	}

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO l1_validator_state (
		subnet_id, validation_id, node_id, balance, weight,
		start_time, end_time, uptime_percentage, active, last_updated, p_chain_id,
		delegator_count, delegated_stake, delegation_fee_percent
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare batch: %w", err)
	}
	defer func() { _ = batch.Abort() }()

	now := time.Now()
	for _, state := range states {
		err = batch.Append(
			state.SubnetID.String(),     // Store as CB58 string
			state.ValidationID.String(), // Store as CB58 string
			state.NodeID.String(),
			state.Balance,
			state.Weight,
			state.StartTime,
			state.EndTime,
			state.Uptime,
			state.Active,
			now,
			pchainID,
			state.DelegatorCount,
			state.DelegatedStake,
			state.DelegationFeePercent,
		)
		if err != nil {
			return fmt.Errorf("failed to append validator state %s: %w", state.ValidationID, err)
		}
	}

	return chwrapper.RetryableBatchSend(batch)
}

// MarkInactiveValidators marks validators as inactive if they are no longer in the current RPC response.
// This handles validators whose staking period has ended and are no longer returned by getCurrentValidators.
func MarkInactiveValidators(ctx context.Context, conn clickhouse.Conn, pchainID uint32, subnetID string, activeValidationIDs []string) error {
	ctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	if len(activeValidationIDs) == 0 {
		return nil
	}

	// First, get all validators currently marked as active in the database for this subnet
	query := `
		SELECT validation_id, node_id, balance, weight, start_time, end_time, uptime_percentage
		FROM l1_validator_state FINAL
		WHERE p_chain_id = ? AND subnet_id = ? AND active = true
	`
	rows, err := conn.Query(ctx, query, pchainID, subnetID)
	if err != nil {
		return fmt.Errorf("failed to query active validators: %w", err)
	}
	defer rows.Close()

	// Build a set of active validation IDs from the RPC response
	activeSet := make(map[string]bool)
	for _, id := range activeValidationIDs {
		activeSet[id] = true
	}

	// Collect validators that are no longer active
	type inactiveValidator struct {
		ValidationID string
		NodeID       string
		Balance      uint64
		Weight       uint64
		StartTime    time.Time
		EndTime      time.Time
		Uptime       float64
	}
	var toDeactivate []inactiveValidator

	for rows.Next() {
		var v inactiveValidator
		if err := rows.Scan(&v.ValidationID, &v.NodeID, &v.Balance, &v.Weight, &v.StartTime, &v.EndTime, &v.Uptime); err != nil {
			return fmt.Errorf("failed to scan validator: %w", err)
		}

		// If this validator is not in the active set from RPC, mark it as inactive
		if !activeSet[v.ValidationID] {
			toDeactivate = append(toDeactivate, v)
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating validators: %w", err)
	}

	if len(toDeactivate) == 0 {
		return nil
	}

	// Insert inactive records (ReplacingMergeTree will keep the latest version)
	batch, err := conn.PrepareBatch(ctx, `INSERT INTO l1_validator_state (
		subnet_id, validation_id, node_id, balance, weight,
		start_time, end_time, uptime_percentage, active, last_updated, p_chain_id
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare batch for inactive validators: %w", err)
	}
	defer func() { _ = batch.Abort() }()

	now := time.Now()
	for _, v := range toDeactivate {
		err = batch.Append(
			subnetID,
			v.ValidationID,
			v.NodeID,
			uint64(0), // balance is 0 when deactivated (remaining gets refunded)
			v.Weight,
			v.StartTime,
			v.EndTime,
			v.Uptime,
			false, // Mark as inactive
			now,
			pchainID,
		)
		if err != nil {
			return fmt.Errorf("failed to append inactive validator %s: %w", v.ValidationID, err)
		}
	}

	if err := chwrapper.RetryableBatchSend(batch); err != nil {
		return fmt.Errorf("failed to send inactive validators batch: %w", err)
	}

	slog.Info("Marked validators as inactive", "count", len(toDeactivate), "subnet_id", subnetID)
	return nil
}

// GetL1Subnets queries ClickHouse for all L1 subnets to monitor
func GetL1Subnets(ctx context.Context, conn clickhouse.Conn, pchainID uint32) ([]ids.ID, error) {
	ctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	query := `
		SELECT DISTINCT subnet_id
		FROM l1_subnets
		WHERE p_chain_id = ?
	`

	rows, err := conn.Query(ctx, query, pchainID)
	if err != nil {
		return nil, fmt.Errorf("failed to query l1_subnets: %w", err)
	}
	defer rows.Close()

	var subnetIDs []ids.ID
	for rows.Next() {
		var subnetIDStr string
		if err := rows.Scan(&subnetIDStr); err != nil {
			return nil, fmt.Errorf("failed to scan subnet_id: %w", err)
		}

		subnetID, err := ids.FromString(subnetIDStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse subnet_id %s: %w", subnetIDStr, err)
		}
		subnetIDs = append(subnetIDs, subnetID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return subnetIDs, nil
}
