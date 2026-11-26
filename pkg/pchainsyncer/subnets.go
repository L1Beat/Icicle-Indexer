package pchainsyncer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"icicle/pkg/chwrapper"
	"icicle/pkg/pchainrpc"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ava-labs/avalanchego/ids"
)

// DiscoverL1SubnetsFromTransactions scans p_chain_txs for ConvertSubnetToL1 and TransformSubnet transactions
// and returns L1 subnet information
func DiscoverL1SubnetsFromTransactions(ctx context.Context, conn clickhouse.Conn, pchainID uint32) ([]L1Subnet, error) {
	ctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	// We also look for TransformSubnet (which creates elastic subnets) as they are effectively L1s
	// or at least have validators we want to track.
	query := `
		SELECT
			tx_data.subnetID as subnet_id,
			coalesce(tx_data.chainID, '') as chain_id,
			CAST(coalesce(tx_data.address, '') AS String) as manager_address,
			block_number,
			block_time
		FROM p_chain_txs
		WHERE p_chain_id = ?
		  AND (tx_type = 'ConvertSubnetToL1' OR tx_type = 'TransformSubnet')
		ORDER BY block_number DESC
	`

	rows, err := conn.Query(ctx, query, pchainID)
	if err != nil {
		return nil, fmt.Errorf("failed to query subnet transactions: %w", err)
	}
	defer rows.Close()

	var subnets []L1Subnet
	seen := make(map[ids.ID]bool) // Deduplicate by subnet_id

	for rows.Next() {
		var subnetIDStr, chainIDStr, managerAddr string
		var blockNumber uint64
		var blockTime time.Time

		if err := rows.Scan(&subnetIDStr, &chainIDStr, &managerAddr, &blockNumber, &blockTime); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Parse subnet ID
		subnetID, err := ids.FromString(subnetIDStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse subnet ID %s: %w", subnetIDStr, err)
		}

		// Skip duplicates (keep the most recent one due to ORDER BY DESC)
		if seen[subnetID] {
			continue
		}
		seen[subnetID] = true

		// Parse chain ID (might be empty for TransformSubnet)
		var chainID ids.ID
		if chainIDStr != "" {
			chainID, err = ids.FromString(chainIDStr)
			if err != nil {
				// Log error but continue? Or fail? failing is safer to detect issues.
				// But TransformSubnet might not have chainID in the same field?
				// TransformSubnet doesn't have ChainID field. It's usually associated with the subnet.
				// We use empty ID if not found.
			}
		}

		// Format validator manager address with 0x prefix
		var vmAddr string
		if managerAddr != "" {
			if strings.HasPrefix(managerAddr, "0x") {
				vmAddr = managerAddr
			} else {
				vmAddr = "0x" + managerAddr
			}
		}

		subnets = append(subnets, L1Subnet{
			SubnetID:                subnetID,
			ChainID:                 chainID,
			ConversionBlock:         blockNumber,
			ConversionTime:          blockTime,
			PChainID:                pchainID,
			ValidatorManagerAddress: vmAddr,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return subnets, nil
}

// Subnet represents a subnet with its lifecycle information
type Subnet struct {
	SubnetID                ids.ID
	CreatedBlock            uint64
	CreatedTime             time.Time
	SubnetType              string // 'legacy', 'l1'
	ChainID                 ids.ID
	ConvertedBlock          uint64
	ConvertedTime           time.Time
	PChainID                uint32
	ValidatorManagerAddress string // 0x-prefixed EVM address of the ValidatorManager contract
}

// SubnetChain represents a blockchain created within a subnet
type SubnetChain struct {
	ChainID      ids.ID
	SubnetID     ids.ID
	ChainName    string
	VMID         ids.ID
	CreatedBlock uint64
	CreatedTime  time.Time
	PChainID     uint32
}

// DiscoverAllSubnets scans p_chain_txs for all subnet-related transactions
// and returns a unified view of all subnets with their current status
func DiscoverAllSubnets(ctx context.Context, conn clickhouse.Conn, pchainID uint32) ([]Subnet, error) {
	ctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	// Map of subnet_id -> Subnet
	subnetsMap := make(map[ids.ID]*Subnet)

	// Helper to update subnet map with earliest occurrence
	updateSubnetMap := func(subnetIDStr string, blockNumber uint64, blockTime time.Time) {
		if subnetIDStr == "" {
			return
		}
		subnetID, err := ids.FromString(subnetIDStr)
		if err != nil {
			return
		}
		existing, exists := subnetsMap[subnetID]
		if !exists || blockNumber < existing.CreatedBlock {
			subnetsMap[subnetID] = &Subnet{
				SubnetID:     subnetID,
				CreatedBlock: blockNumber,
				CreatedTime:  blockTime,
				SubnetType:   "legacy",
				PChainID:     pchainID,
			}
		}
	}

	// First, get the max block we've already processed from the subnets table
	// This allows incremental discovery instead of re-scanning the entire history
	var lastProcessedBlock uint64
	err := conn.QueryRow(ctx, `
		SELECT COALESCE(max(created_block), 0) FROM subnets FINAL WHERE p_chain_id = ?
	`, pchainID).Scan(&lastProcessedBlock)
	if err != nil {
		slog.Warn("Could not get last processed block, will scan from start", "error", err)
		lastProcessedBlock = 0
	}

	// Query each transaction type separately, only for blocks after last processed
	txTypes := []string{"CreateChain", "AddSubnetValidator", "ConvertSubnetToL1", "TransformSubnet"}

	for _, txType := range txTypes {
		// Only query new blocks to avoid memory issues with full table scans
		query := `
			SELECT
				CAST(tx_data.subnetID AS String) as subnet_id,
				block_number,
				block_time
			FROM p_chain_txs
			WHERE p_chain_id = ?
			  AND tx_type = ?
			  AND block_number > ?
			  AND tx_data.subnetID != ''
			ORDER BY block_number ASC
		`

		rows, err := conn.Query(ctx, query, pchainID, txType, lastProcessedBlock)
		if err != nil {
			return nil, fmt.Errorf("failed to query %s transactions: %w", txType, err)
		}

		for rows.Next() {
			var subnetIDStr string
			var blockNumber uint64
			var blockTime time.Time

			if err := rows.Scan(&subnetIDStr, &blockNumber, &blockTime); err != nil {
				rows.Close()
				return nil, fmt.Errorf("failed to scan %s row: %w", txType, err)
			}
			updateSubnetMap(subnetIDStr, blockNumber, blockTime)
		}
		rows.Close()

		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("%s discovery rows error: %w", txType, err)
		}
	}

	// Now check for conversions (TransformSubnet and ConvertSubnetToL1)
	// These are relatively small tables so we can scan them fully
	conversionQuery := `
		SELECT
			CAST(tx_data.subnetID AS String) as subnet_id,
			tx_type,
			CAST(coalesce(tx_data.chainID, '') AS String) as chain_id,
			CAST(coalesce(tx_data.address, '') AS String) as manager_address,
			block_number,
			block_time
		FROM p_chain_txs
		WHERE p_chain_id = ?
		  AND tx_type IN ('TransformSubnet', 'ConvertSubnetToL1')
		  AND tx_data.subnetID != ''
		ORDER BY block_number ASC
	`

	rows, err := conn.Query(ctx, conversionQuery, pchainID)
	if err != nil {
		return nil, fmt.Errorf("failed to query conversion transactions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var subnetIDStr, txType, chainIDStr, managerAddr string
		var blockNumber uint64
		var blockTime time.Time

		if err := rows.Scan(&subnetIDStr, &txType, &chainIDStr, &managerAddr, &blockNumber, &blockTime); err != nil {
			return nil, fmt.Errorf("failed to scan conversion row: %w", err)
		}

		// Skip empty subnet IDs
		if subnetIDStr == "" {
			continue
		}

		subnetID, err := ids.FromString(subnetIDStr)
		if err != nil {
			// Skip invalid subnet IDs (likely CB58 encoding issues)
			continue
		}

		subnet, exists := subnetsMap[subnetID]
		if !exists {
			// Subnet was converted but never had a CreateSubnet tx (shouldn't happen normally)
			// Create it anyway with conversion as creation
			subnet = &Subnet{
				SubnetID:     subnetID,
				CreatedBlock: blockNumber,
				CreatedTime:  blockTime,
				PChainID:     pchainID,
			}
			subnetsMap[subnetID] = subnet
		}

		// Update subnet type based on transaction
		if txType == "TransformSubnet" {
			subnet.SubnetType = "elastic"
			subnet.ConvertedBlock = blockNumber
			subnet.ConvertedTime = blockTime
		} else if txType == "ConvertSubnetToL1" {
			subnet.SubnetType = "l1"
			subnet.ConvertedBlock = blockNumber
			subnet.ConvertedTime = blockTime

			// Parse chain ID for L1 conversions
			if chainIDStr != "" {
				chainID, err := ids.FromString(chainIDStr)
				if err == nil {
					subnet.ChainID = chainID
				}
			}

			// Set validator manager address
			if managerAddr != "" {
				if strings.HasPrefix(managerAddr, "0x") {
					subnet.ValidatorManagerAddress = managerAddr
				} else {
					subnet.ValidatorManagerAddress = "0x" + managerAddr
				}
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("conversion rows error: %w", err)
	}

	// Convert map to slice
	subnets := make([]Subnet, 0, len(subnetsMap))
	for _, subnet := range subnetsMap {
		subnets = append(subnets, *subnet)
	}

	return subnets, nil
}

// DiscoverSubnetChains scans p_chain_txs for CreateChain transactions.
// The tx_id of a CreateChainTx IS the blockchain ID on Avalanche.
func DiscoverSubnetChains(ctx context.Context, conn clickhouse.Conn, pchainID uint32) ([]SubnetChain, error) {
	ctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	query := `
		SELECT
			tx_id as chain_id,
			CAST(tx_data.subnetID AS String) as subnet_id,
			CAST(coalesce(tx_data.chainName, '') AS String) as chain_name,
			CAST(coalesce(tx_data.vmID, '') AS String) as vm_id,
			block_number,
			block_time
		FROM p_chain_txs
		WHERE p_chain_id = ?
		  AND tx_type = 'CreateChain'
		  AND tx_data.subnetID != ''
		ORDER BY block_number ASC
	`

	rows, err := conn.Query(ctx, query, pchainID)
	if err != nil {
		return nil, fmt.Errorf("failed to query chain info: %w", err)
	}
	defer rows.Close()

	var chains []SubnetChain
	seen := make(map[ids.ID]bool)

	for rows.Next() {
		var chainIDStr, subnetIDStr, chainName, vmIDStr string
		var blockNumber uint64
		var blockTime time.Time

		if err := rows.Scan(&chainIDStr, &subnetIDStr, &chainName, &vmIDStr, &blockNumber, &blockTime); err != nil {
			return nil, fmt.Errorf("failed to scan chain row: %w", err)
		}

		// Skip empty or invalid chain IDs
		if chainIDStr == "" || subnetIDStr == "" {
			continue
		}

		chainID, err := ids.FromString(chainIDStr)
		if err != nil {
			continue
		}

		// Skip duplicates
		if seen[chainID] {
			continue
		}
		seen[chainID] = true

		subnetID, err := ids.FromString(subnetIDStr)
		if err != nil {
			continue
		}

		var vmID ids.ID
		if vmIDStr != "" {
			vmID, err = ids.FromString(vmIDStr)
			if err != nil {
				vmID = ids.Empty
			}
		}

		chains = append(chains, SubnetChain{
			ChainID:      chainID,
			SubnetID:     subnetID,
			ChainName:    chainName,
			VMID:         vmID,
			CreatedBlock: blockNumber,
			CreatedTime:  blockTime,
			PChainID:     pchainID,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("chain discovery rows error: %w", err)
	}

	return chains, nil
}

// InsertSubnets inserts or updates subnet records in the unified subnets table
func InsertSubnets(ctx context.Context, conn clickhouse.Conn, subnets []Subnet) error {
	ctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	if len(subnets) == 0 {
		return nil
	}

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO subnets (
		subnet_id, created_block, created_time, subnet_type,
		chain_id, converted_block, converted_time, validator_manager_address, validator_manager_owner, p_chain_id, last_updated
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare batch: %w", err)
	}
	defer func() { _ = batch.Abort() }()

	now := time.Now()
	for _, subnet := range subnets {
		err = batch.Append(
			subnet.SubnetID.String(),
			subnet.CreatedBlock,
			subnet.CreatedTime,
			subnet.SubnetType,
			subnet.ChainID.String(),
			subnet.ConvertedBlock,
			subnet.ConvertedTime,
			subnet.ValidatorManagerAddress,
			"", // validator_manager_owner populated by owner syncer
			subnet.PChainID,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to append subnet %s: %w", subnet.SubnetID, err)
		}
	}

	return chwrapper.RetryableBatchSend(batch)
}

// Avalanche mainnet genesis timestamp: September 21, 2020 10:00:00 UTC
var AvalancheGenesisTime = time.Unix(1600714800, 0)

// InsertPrimaryNetwork inserts the Primary Network as a special genesis subnet
func InsertPrimaryNetwork(ctx context.Context, conn clickhouse.Conn, pchainID uint32) error {
	ctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	// Primary Network subnet ID (all 1s in CB58)
	primarySubnetID := "11111111111111111111111111111111LpoYY"

	subnet := Subnet{
		SubnetID:     ids.Empty, // Will use string directly
		CreatedBlock: 0,         // Genesis
		CreatedTime:  AvalancheGenesisTime,
		SubnetType:   "primary", // Special type for Primary Network
		PChainID:     pchainID,
	}

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO subnets (
		subnet_id, created_block, created_time, subnet_type,
		chain_id, converted_block, converted_time, validator_manager_address, validator_manager_owner, p_chain_id, last_updated
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare batch: %w", err)
	}
	defer func() { _ = batch.Abort() }()

	now := time.Now()
	err = batch.Append(
		primarySubnetID,
		subnet.CreatedBlock,
		subnet.CreatedTime,
		subnet.SubnetType,
		"",
		uint64(0),
		AvalancheGenesisTime,
		"", // no validator manager for primary network
		"", // no validator manager owner for primary network
		subnet.PChainID,
		now,
	)
	if err != nil {
		return fmt.Errorf("failed to append primary network: %w", err)
	}

	return chwrapper.RetryableBatchSend(batch)
}

// InsertPrimaryNetworkChains inserts C-Chain, X-Chain, and P-Chain
func InsertPrimaryNetworkChains(ctx context.Context, conn clickhouse.Conn, pchainID uint32) error {
	ctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	primarySubnetID := "11111111111111111111111111111111LpoYY"

	chains := []struct {
		chainID   string
		chainName string
		vmID      string
	}{
		{
			chainID:   "2q9e4r6Mu3U68nU1fYjgbR6JvwrRx36CohpAX5UQxse55x1Q5", // C-Chain
			chainName: "C-Chain",
			vmID:      "mgj786NP7uDwBCcq6YwThhaN8FLyybkCa4zBWTQbNgmK6k9A6", // EVM
		},
	}

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO subnet_chains (
		chain_id, subnet_id, chain_name, vm_id,
		created_block, created_time, p_chain_id, last_updated
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare batch: %w", err)
	}
	defer func() { _ = batch.Abort() }()

	now := time.Now()
	for _, chain := range chains {
		err = batch.Append(
			chain.chainID,
			primarySubnetID,
			chain.chainName,
			chain.vmID,
			uint64(0), // Genesis
			AvalancheGenesisTime,
			pchainID,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to append chain %s: %w", chain.chainName, err)
		}
	}

	return chwrapper.RetryableBatchSend(batch)
}

// InsertSubnetChains inserts or updates subnet chain records
func InsertSubnetChains(ctx context.Context, conn clickhouse.Conn, chains []SubnetChain) error {
	ctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	if len(chains) == 0 {
		return nil
	}

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO subnet_chains (
		chain_id, subnet_id, chain_name, vm_id,
		created_block, created_time, p_chain_id, last_updated
	)`)
	if err != nil {
		return fmt.Errorf("failed to prepare batch: %w", err)
	}
	defer func() { _ = batch.Abort() }()

	now := time.Now()
	for _, chain := range chains {
		err = batch.Append(
			chain.ChainID.String(),
			chain.SubnetID.String(),
			chain.ChainName,
			chain.VMID.String(),
			chain.CreatedBlock,
			chain.CreatedTime,
			chain.PChainID,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to append chain %s: %w", chain.ChainID, err)
		}
	}

	return chwrapper.RetryableBatchSend(batch)
}

// BackfillSubnetChainsFromRPC fetches all blockchains from platform.getBlockchains
// and inserts the ones that DiscoverSubnetChains did not find, so chains whose
// CreateChainTx is missing from p_chain_txs still get a row. getBlockchains has
// no creation block or time, so those rows carry block 0 and a zero time.
//
// Chains in known (found from their CreateChainTx) and Primary Network chains
// (seeded with the genesis time by InsertPrimaryNetworkChains) are skipped:
// re-inserting them here, with a newer last_updated, used to replace their real
// created_block/created_time with zeros on every validator cycle.
func BackfillSubnetChainsFromRPC(ctx context.Context, conn clickhouse.Conn, fetcher *pchainrpc.Fetcher, pchainID uint32, known map[string]bool) (int, error) {
	resp, err := fetcher.GetBlockchains(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get blockchains from RPC: %w", err)
	}

	if len(resp.Blockchains) == 0 {
		return 0, nil
	}

	// Bounded per-op: the GetBlockchains RPC above rides the parent ctx (it has
	// its own HTTP timeout), but the insert needs a write deadline so a severed
	// connection can't park this goroutine (see SyncL1ValidatorRefunds).
	wctx, wcancel := chwrapper.WriteContext(ctx)
	defer wcancel()
	batch, err := conn.PrepareBatch(wctx, `INSERT INTO subnet_chains (
		chain_id, subnet_id, chain_name, vm_id,
		created_block, created_time, p_chain_id, last_updated
	)`)
	if err != nil {
		return 0, fmt.Errorf("failed to prepare batch: %w", err)
	}
	defer func() { _ = batch.Abort() }()

	now := time.Now()
	missing := missingRPCChains(resp.Blockchains, known)
	if len(missing) == 0 {
		return 0, nil
	}

	for _, bc := range missing {
		err = batch.Append(
			bc.ID,
			bc.SubnetID,
			bc.Name,
			bc.VMID,
			uint64(0),
			time.Time{},
			pchainID,
			now,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to append chain %s: %w", bc.ID, err)
		}
	}

	if err := chwrapper.RetryableBatchSend(batch); err != nil {
		return 0, fmt.Errorf("failed to send batch: %w", err)
	}

	return len(missing), nil
}

const primaryNetworkSubnetID = "11111111111111111111111111111111LpoYY"

// missingRPCChains returns the getBlockchains entries that need a row from the
// RPC backfill: not on the Primary Network and not already found from a
// CreateChainTx.
func missingRPCChains(all []pchainrpc.BlockchainInfo, known map[string]bool) []pchainrpc.BlockchainInfo {
	var out []pchainrpc.BlockchainInfo
	for _, bc := range all {
		if bc.SubnetID == primaryNetworkSubnetID || known[bc.ID] {
			continue
		}
		out = append(out, bc)
	}
	return out
}
