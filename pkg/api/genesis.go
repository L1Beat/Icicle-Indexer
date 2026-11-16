package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	avagenesis "github.com/ava-labs/avalanchego/genesis"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/formatting/address"
	"github.com/ava-labs/avalanchego/utils/hashing"
	"github.com/ava-labs/avalanchego/vms/platformvm/block"
)

// The mainnet genesis is served from the config every avalanchego node boots
// from (genesis.MainnetConfig), not from ClickHouse. The indexers start at
// block 1, and a genesis row in raw_blocks would carry the C-Chain's 0
// timestamp into every time-based metric.

// delegationFeeDenominator is the P-Chain's fee scale: 1,000,000 = 100%.
const delegationFeeDenominator = 1_000_000

// GenesisUnlock is one locked tranche of an allocation.
type GenesisUnlock struct {
	Amount     string    `json:"amount"`      // nAVAX
	UnlockTime time.Time `json:"unlock_time"` // locked on the P-Chain until this time
}

// GenesisAllocation is one address's share of the initial supply.
type GenesisAllocation struct {
	Index int `json:"index"` // position in the genesis config
	// Address is the bech32 AVAX address without a chain prefix: the unlocked
	// amount was created on the X-Chain and the locked tranches on the P-Chain.
	Address    string `json:"address"`
	ETHAddress string `json:"eth_address"`
	// XChainAmount was spendable on the X-Chain at genesis.
	XChainAmount string `json:"x_chain_amount"` // nAVAX
	// UnlockSchedule was locked on the P-Chain until each unlock time. For an
	// initially staked address these tranches were staked by the genesis
	// validators instead.
	UnlockSchedule  []GenesisUnlock `json:"unlock_schedule"`
	Total           string          `json:"total"` // nAVAX
	InitiallyStaked bool            `json:"initially_staked"`
}

// GenesisValidator is one of the network's initial validators.
type GenesisValidator struct {
	NodeID               string    `json:"node_id"`
	RewardAddress        string    `json:"reward_address"`
	DelegationFeePercent float64   `json:"delegation_fee_percent"`
	StartTime            time.Time `json:"start_time"`
	EndTime              time.Time `json:"end_time"`
}

// GenesisSupply splits the initial supply by where it was created.
type GenesisSupply struct {
	Total            string `json:"total"`            // nAVAX
	XChainUnlocked   string `json:"x_chain_unlocked"` // spendable on the X-Chain at genesis
	PChainLocked     string `json:"p_chain_locked"`   // locked on the P-Chain until each tranche's unlock time
	InitiallyStaked  string `json:"initially_staked"` // staked by the genesis validators
	AllocationCount  int    `json:"allocation_count"`
	StakedAddressCnt int    `json:"initially_staked_address_count"`
}

// GenesisUnlockBucket is the total locked amount that unlocked at one time.
type GenesisUnlockBucket struct {
	UnlockTime time.Time `json:"unlock_time"`
	Amount     string    `json:"amount"` // nAVAX
}

// GenesisOverview is the /data/genesis payload.
type GenesisOverview struct {
	NetworkID             uint32                `json:"network_id"`
	Message               string                `json:"message"`
	StartTime             time.Time             `json:"start_time"`
	PChainGenesisBlockID  string                `json:"p_chain_genesis_block_id"`
	XChainID              string                `json:"x_chain_id"`
	CChainID              string                `json:"c_chain_id"`
	AVAXAssetID           string                `json:"avax_asset_id"`
	Supply                GenesisSupply         `json:"supply"`
	UnlockSchedule        []GenesisUnlockBucket `json:"unlock_schedule"`
	InitialStakeDuration  int64                 `json:"initial_stake_duration_seconds"`
	InitialValidators     []GenesisValidator    `json:"initial_validators"`
	InitiallyStakedFunds  []string              `json:"initially_staked_addresses"`
	CChainGenesis         json.RawMessage       `json:"c_chain_genesis" swaggertype:"object"`
	CChainGenesisGasLimit uint64                `json:"-"`
}

type genesisData struct {
	overview    GenesisOverview
	allocations []GenesisAllocation
}

var (
	genesisOnce   sync.Once
	genesisCached *genesisData
	genesisErr    error
)

func mainnetGenesis() (*genesisData, error) {
	genesisOnce.Do(func() {
		genesisCached, genesisErr = buildGenesis(&avagenesis.MainnetConfig)
	})
	return genesisCached, genesisErr
}

func buildGenesis(cfg *avagenesis.Config) (*genesisData, error) {
	hrp := constants.GetHRP(cfg.NetworkID)
	bech := func(id ids.ShortID) (string, error) { return address.FormatBech32(hrp, id.Bytes()) }

	pGenesisBytes, avaxAssetID, err := avagenesis.FromConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build genesis: %w", err)
	}
	genesisBlock, err := block.NewApricotCommitBlock(hashing.ComputeHash256Array(pGenesisBytes), 0)
	if err != nil {
		return nil, fmt.Errorf("genesis block: %w", err)
	}
	xChain, err := avagenesis.VMGenesis(pGenesisBytes, constants.AVMID)
	if err != nil {
		return nil, err
	}
	cChain, err := avagenesis.VMGenesis(pGenesisBytes, constants.EVMID)
	if err != nil {
		return nil, err
	}

	var cGenesis struct {
		GasLimit string `json:"gasLimit"`
	}
	if err := json.Unmarshal([]byte(cfg.CChainGenesis), &cGenesis); err != nil {
		return nil, fmt.Errorf("parse C-Chain genesis: %w", err)
	}
	gasLimit, err := strconv.ParseUint(strings.TrimPrefix(cGenesis.GasLimit, "0x"), 16, 64)
	if err != nil {
		return nil, fmt.Errorf("parse C-Chain gas limit: %w", err)
	}

	staked := make(map[ids.ShortID]bool, len(cfg.InitialStakedFunds))
	stakedAddrs := make([]string, 0, len(cfg.InitialStakedFunds))
	for _, a := range cfg.InitialStakedFunds {
		staked[a] = true
		s, err := bech(a)
		if err != nil {
			return nil, err
		}
		stakedAddrs = append(stakedAddrs, s)
	}

	var total, xUnlocked, pLocked, initStaked uint64
	byUnlock := map[uint64]uint64{}
	allocs := make([]GenesisAllocation, 0, len(cfg.Allocations))
	for i, a := range cfg.Allocations {
		addr, err := bech(a.AVAXAddr)
		if err != nil {
			return nil, err
		}
		sum := a.InitialAmount
		xUnlocked += a.InitialAmount
		sched := make([]GenesisUnlock, 0, len(a.UnlockSchedule))
		for _, u := range a.UnlockSchedule {
			sum += u.Amount
			if staked[a.AVAXAddr] {
				initStaked += u.Amount
			} else {
				pLocked += u.Amount
			}
			byUnlock[u.Locktime] += u.Amount
			sched = append(sched, GenesisUnlock{
				Amount:     strconv.FormatUint(u.Amount, 10),
				UnlockTime: time.Unix(int64(u.Locktime), 0).UTC(),
			})
		}
		total += sum
		allocs = append(allocs, GenesisAllocation{
			Index:           i,
			Address:         addr,
			ETHAddress:      "0x" + hex.EncodeToString(a.ETHAddr.Bytes()),
			XChainAmount:    strconv.FormatUint(a.InitialAmount, 10),
			UnlockSchedule:  sched,
			Total:           strconv.FormatUint(sum, 10),
			InitiallyStaked: staked[a.AVAXAddr],
		})
	}

	buckets := make([]GenesisUnlockBucket, 0, len(byUnlock))
	for t, amt := range byUnlock {
		buckets = append(buckets, GenesisUnlockBucket{
			UnlockTime: time.Unix(int64(t), 0).UTC(),
			Amount:     strconv.FormatUint(amt, 10),
		})
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].UnlockTime.Before(buckets[j].UnlockTime) })

	// Same validator periods as avalanchego's FromConfig: each initial
	// validator ends one offset earlier than the one before it.
	start := time.Unix(int64(cfg.StartTime), 0).UTC()
	end := start.Add(time.Duration(cfg.InitialStakeDuration) * time.Second)
	offset := time.Duration(cfg.InitialStakeDurationOffset) * time.Second
	validators := make([]GenesisValidator, 0, len(cfg.InitialStakers))
	for i, s := range cfg.InitialStakers {
		reward, err := bech(s.RewardAddress)
		if err != nil {
			return nil, err
		}
		validators = append(validators, GenesisValidator{
			NodeID:               s.NodeID.String(),
			RewardAddress:        "P-" + reward,
			DelegationFeePercent: float64(s.DelegationFee) * 100 / delegationFeeDenominator,
			StartTime:            start,
			EndTime:              end.Add(-time.Duration(i) * offset),
		})
	}

	return &genesisData{
		overview: GenesisOverview{
			NetworkID:            cfg.NetworkID,
			Message:              cfg.Message,
			StartTime:            start,
			PChainGenesisBlockID: genesisBlock.ID().String(),
			XChainID:             xChain.ID().String(),
			CChainID:             cChain.ID().String(),
			AVAXAssetID:          avaxAssetID.String(),
			Supply: GenesisSupply{
				Total:            strconv.FormatUint(total, 10),
				XChainUnlocked:   strconv.FormatUint(xUnlocked, 10),
				PChainLocked:     strconv.FormatUint(pLocked, 10),
				InitiallyStaked:  strconv.FormatUint(initStaked, 10),
				AllocationCount:  len(cfg.Allocations),
				StakedAddressCnt: len(cfg.InitialStakedFunds),
			},
			UnlockSchedule:        buckets,
			InitialStakeDuration:  int64(cfg.InitialStakeDuration),
			InitialValidators:     validators,
			InitiallyStakedFunds:  stakedAddrs,
			CChainGenesis:         json.RawMessage(cfg.CChainGenesis),
			CChainGenesisGasLimit: gasLimit,
		},
		allocations: allocs,
	}, nil
}

// matches reports whether q names this allocation: a bech32
// address with or without an X-/P- prefix, or a 0x ETH address.
func (a GenesisAllocation) matches(q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if strings.HasPrefix(q, "0x") {
		return q == a.ETHAddress
	}
	if i := strings.Index(q, "-"); i >= 0 {
		q = q[i+1:]
	}
	return q == a.Address
}
