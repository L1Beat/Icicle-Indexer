package pchainsyncer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"icicle/pkg/chwrapper"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp/message"
)

// L1ValidatorHistory represents a historical L1 validator record
type L1ValidatorHistory struct {
	SubnetID              string
	NodeID                string
	ValidationID          string // Computed validation ID (CB58)
	CreatedTxID           string
	CreatedTxType         string
	CreatedBlock          uint64
	CreatedTime           time.Time
	InitialBalance        uint64
	InitialWeight         uint64
	BLSPublicKey          string
	RemainingBalanceOwner string // P-Chain address for refunds (CB58)
	PChainID              uint32
}

// ConvertSubnetValidator represents a validator from ConvertSubnetToL1Tx JSON
type ConvertSubnetValidator struct {
	NodeID  string `json:"nodeID"`
	Weight  uint64 `json:"weight"`
	Balance uint64 `json:"balance"`
	Signer  struct {
		PublicKey string `json:"publicKey"`
	} `json:"signer"`
	RemainingBalanceOwner struct {
		Threshold uint32   `json:"threshold"`
		Addresses []string `json:"addresses"`
	} `json:"remainingBalanceOwner"`
}

// DiscoverL1ValidatorHistory discovers all L1 validators from historical transactions
func DiscoverL1ValidatorHistory(ctx context.Context, conn clickhouse.Conn, pchainID uint32) ([]L1ValidatorHistory, error) {
	ctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	// Get last processed block for incremental discovery
	var lastProcessedBlock uint64
	err := conn.QueryRow(ctx, `
		SELECT COALESCE(max(created_block), 0) FROM l1_validator_history FINAL WHERE p_chain_id = ?
	`, pchainID).Scan(&lastProcessedBlock)
	if err != nil {
		slog.Warn("Could not get last processed block for validator history", "error", err)
		lastProcessedBlock = 0
	}

	// Query validators from ConvertSubnetToL1Tx transactions (only new blocks)
	// Fetch the raw validators JSON and parse it in Go for reliable extraction
	query := `
		SELECT
			tx_id,
			block_number,
			block_time,
			toString(tx_data.subnetID) as subnet_id,
			toString(tx_data.validators) as validators_json
		FROM p_chain_txs
		WHERE p_chain_id = ?
		  AND tx_type = 'ConvertSubnetToL1'
		  AND block_number > ?
		ORDER BY block_number ASC
	`

	rows, err := conn.Query(ctx, query, pchainID, lastProcessedBlock)
	if err != nil {
		return nil, fmt.Errorf("failed to query validator history: %w", err)
	}
	defer rows.Close()

	var validators []L1ValidatorHistory
	for rows.Next() {
		var txID, subnetID, validatorsJSON string
		var blockNumber uint64
		var blockTime time.Time

		if err := rows.Scan(&txID, &blockNumber, &blockTime, &subnetID, &validatorsJSON); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Parse the validators JSON array in Go
		// ClickHouse toString() on JSON array produces ['{"json":...}', '{"json":...}'] format
		// We need to convert this to proper JSON array format
		validatorsJSON = convertClickHouseArrayToJSON(validatorsJSON)

		var txValidators []ConvertSubnetValidator
		if err := json.Unmarshal([]byte(validatorsJSON), &txValidators); err != nil {
			slog.Warn("Failed to parse validators JSON", "tx_id", txID, "error", err, "json_preview", validatorsJSON[:min(200, len(validatorsJSON))])
			continue
		}

		// Parse subnet ID for computing validation_id
		subnetIDParsed, err := ids.FromString(subnetID)
		if err != nil {
			slog.Warn("Failed to parse subnet ID", "subnet_id", subnetID, "tx_id", txID, "error", err)
			continue
		}

		// Create a validator record for each validator in the array
		for i, v := range txValidators {
			// Convert hex node ID to CB58 format
			nodeIDCB58, err := hexToNodeID(v.NodeID)
			if err != nil {
				slog.Warn("Failed to convert node ID to CB58", "node_id", v.NodeID, "tx_id", txID, "error", err)
				continue
			}

			// Compute validation_id: subnetID.Append(validatorIndex) as per ACP-77
			validationID := subnetIDParsed.Append(uint32(i))

			// Extract remainingBalanceOwner address (first address if available)
			var remainingBalanceOwner string
			if len(v.RemainingBalanceOwner.Addresses) > 0 {
				remainingBalanceOwner = v.RemainingBalanceOwner.Addresses[0]
			}

			validators = append(validators, L1ValidatorHistory{
				SubnetID:              subnetID,
				NodeID:                nodeIDCB58,
				ValidationID:          validationID.String(),
				CreatedTxID:           txID,
				CreatedTxType:         "ConvertSubnetToL1",
				CreatedBlock:          blockNumber,
				CreatedTime:           blockTime,
				InitialBalance:        v.Balance,
				InitialWeight:         v.Weight,
				BLSPublicKey:          v.Signer.PublicKey,
				RemainingBalanceOwner: remainingBalanceOwner,
				PChainID:              pchainID,
			})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	// Check if we've ever processed RegisterL1Validator transactions
	var registerCount uint64
	_ = conn.QueryRow(ctx, `
		SELECT count(*) FROM l1_validator_history FINAL
		WHERE p_chain_id = ? AND created_tx_type = 'RegisterL1Validator'
	`, pchainID).Scan(&registerCount)

	// If no RegisterL1Validator records exist, process all of them (one-time backfill)
	// Otherwise, only process new blocks
	var registerQuery string
	var registerRows driver.Rows
	if registerCount == 0 {
		slog.Info("No RegisterL1Validator records found, performing full backfill")
		registerQuery = `
			SELECT
				tx_id,
				block_number,
				block_time,
				toString(tx_data.message) as message,
				toUInt64OrZero(toString(tx_data.balance)) as balance
			FROM p_chain_txs
			WHERE p_chain_id = ?
			  AND tx_type = 'RegisterL1Validator'
			ORDER BY block_number ASC
		`
		registerRows, err = conn.Query(ctx, registerQuery, pchainID)
	} else {
		registerQuery = `
			SELECT
				tx_id,
				block_number,
				block_time,
				toString(tx_data.message) as message,
				toUInt64OrZero(toString(tx_data.balance)) as balance
			FROM p_chain_txs
			WHERE p_chain_id = ?
			  AND tx_type = 'RegisterL1Validator'
			  AND block_number > ?
			ORDER BY block_number ASC
		`
		registerRows, err = conn.Query(ctx, registerQuery, pchainID, lastProcessedBlock)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query RegisterL1Validator txs: %w", err)
	}
	defer registerRows.Close()

	for registerRows.Next() {
		var txID, messageHex string
		var blockNumber, balance uint64
		var blockTime time.Time

		if err := registerRows.Scan(&txID, &blockNumber, &blockTime, &messageHex, &balance); err != nil {
			slog.Warn("Failed to scan RegisterL1Validator row", "error", err)
			continue
		}

		// Parse the Warp message to extract validator info
		innerPayload, err := parseWarpPayload(messageHex)
		if err != nil {
			slog.Warn("Failed to parse Warp message", "tx_id", txID, "error", err)
			continue
		}

		// Parse the inner payload as RegisterL1Validator message
		regMsg, err := message.ParseRegisterL1Validator(innerPayload)
		if err != nil {
			slog.Warn("Failed to parse RegisterL1Validator payload", "tx_id", txID, "error", err)
			continue
		}

		// Validate NodeID is not empty
		if len(regMsg.NodeID) == 0 {
			slog.Warn("Empty NodeID in RegisterL1Validator tx, skipping", "tx_id", txID)
			continue
		}

		// Convert node ID bytes to CB58 format
		var nodeID ids.NodeID
		if len(regMsg.NodeID) != len(nodeID) {
			slog.Warn("Invalid NodeID length, skipping", "length", len(regMsg.NodeID), "expected", len(nodeID), "tx_id", txID)
			continue
		}
		copy(nodeID[:], regMsg.NodeID)

		// Validate the resulting NodeID is not all zeros
		var zeroNodeID ids.NodeID
		if nodeID == zeroNodeID {
			slog.Warn("All-zero NodeID in RegisterL1Validator tx, skipping", "tx_id", txID)
			continue
		}

		nodeIDStr := nodeID.String()

		// Compute validation_id using regMsg.ValidationID()
		validationID := regMsg.ValidationID()

		// Extract remainingBalanceOwner address (first address if available)
		var remainingBalanceOwner string
		if len(regMsg.RemainingBalanceOwner.Addresses) > 0 {
			remainingBalanceOwner = regMsg.RemainingBalanceOwner.Addresses[0].String()
		}

		slog.Debug("RegisterL1Validator tx", "tx_id", txID, "node_id", nodeIDStr, "subnet_id", regMsg.SubnetID.String(), "weight", regMsg.Weight, "validation_id", validationID.String())

		// Extract validator info
		validators = append(validators, L1ValidatorHistory{
			SubnetID:              regMsg.SubnetID.String(),
			NodeID:                nodeIDStr,
			ValidationID:          validationID.String(),
			CreatedTxID:           txID,
			CreatedTxType:         "RegisterL1Validator",
			CreatedBlock:          blockNumber,
			CreatedTime:           blockTime,
			InitialBalance:        balance,
			InitialWeight:         regMsg.Weight,
			BLSPublicKey:          "0x" + hex.EncodeToString(regMsg.BLSPublicKey[:]),
			RemainingBalanceOwner: remainingBalanceOwner,
			PChainID:              pchainID,
		})
	}

	if err := registerRows.Err(); err != nil {
		return nil, fmt.Errorf("RegisterL1Validator rows error: %w", err)
	}

	return validators, nil
}

// InsertL1ValidatorHistory inserts historical validator records
func InsertL1ValidatorHistory(ctx context.Context, conn clickhouse.Conn, validators []L1ValidatorHistory) error {
	ctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	if len(validators) == 0 {
		return nil
	}

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO l1_validator_history (
		subnet_id, node_id, validation_id, created_tx_id, created_tx_type, created_block, created_time,
		initial_balance, initial_weight, bls_public_key, remaining_balance_owner, p_chain_id, last_updated
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare batch: %w", err)
	}
	defer func() { _ = batch.Abort() }()

	now := time.Now()
	for _, v := range validators {
		err = batch.Append(
			v.SubnetID,
			v.NodeID,
			v.ValidationID,
			v.CreatedTxID,
			v.CreatedTxType,
			v.CreatedBlock,
			v.CreatedTime,
			v.InitialBalance,
			v.InitialWeight,
			v.BLSPublicKey,
			v.RemainingBalanceOwner,
			v.PChainID,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to append validator %s: %w", v.NodeID, err)
		}
	}

	return chwrapper.RetryableBatchSend(batch)
}

// BackfillValidatorStateFromHistory inserts history-discovered validators into l1_validator_state
// so the API can query a single table. Only inserts validators not already present in state.
func BackfillValidatorStateFromHistory(ctx context.Context, conn clickhouse.Conn, pchainID uint32, validators []L1ValidatorHistory) error {
	ctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	if len(validators) == 0 {
		return nil
	}

	// Get existing validators in state to avoid overwriting RPC-synced data
	existing := make(map[string]bool)
	rows, err := conn.Query(ctx, `
		SELECT DISTINCT node_id FROM l1_validator_state FINAL WHERE p_chain_id = ?
	`, pchainID)
	if err != nil {
		return fmt.Errorf("failed to query existing validators: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			return fmt.Errorf("failed to scan node_id: %w", err)
		}
		existing[nodeID] = true
	}

	// Filter to only new validators
	var toInsert []L1ValidatorHistory
	for _, v := range validators {
		if !existing[v.NodeID] {
			toInsert = append(toInsert, v)
		}
	}

	if len(toInsert) == 0 {
		return nil
	}

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO l1_validator_state (
		subnet_id, validation_id, node_id, balance, weight,
		start_time, end_time, uptime_percentage, active, last_updated, p_chain_id,
		initial_deposit
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare batch: %w", err)
	}
	defer func() { _ = batch.Abort() }()

	now := time.Now()
	zeroTime := time.Unix(0, 0).UTC()
	for _, v := range toInsert {
		err = batch.Append(
			v.SubnetID,
			v.ValidationID,
			v.NodeID,
			uint64(0),       // balance (unknown, will be updated by RPC sync if active)
			v.InitialWeight, // weight from creation tx
			v.CreatedTime,   // start_time = creation time
			zeroTime,        // end_time unknown
			float64(0),      // uptime unknown
			false,           // inactive until confirmed by RPC
			now,
			pchainID,
			v.InitialBalance, // initial_deposit from creation tx
		)
		if err != nil {
			return fmt.Errorf("failed to append validator %s: %w", v.NodeID, err)
		}
	}

	if err := chwrapper.RetryableBatchSend(batch); err != nil {
		return fmt.Errorf("failed to send batch: %w", err)
	}

	slog.Info("Backfilled validator state from history", "count", len(toInsert))
	return nil
}
