// Package lending implements a real-time lending-position health feed for
// Avalanche C-Chain. It discovers borrow positions from event logs the indexer
// already stores in raw_logs, reads health directly from the protocols through
// Multicall3 on a tiered schedule, and exposes a ranked liquidation-risk view.
//
// This package builds the data feed only. Liquidation execution, flash loans,
// collateral-swap routing, and transaction submission are out of scope.
package lending

import (
	"math/big"
	"strings"
)

// Protocol identifies a supported lending market.
type Protocol string

const (
	ProtocolAaveV3 Protocol = "aave-v3"
	ProtocolBenqi  Protocol = "benqi"
	ProtocolAaveV4 Protocol = "aave-v4"
)

// Aave V4 keeps one independent position per (spoke, user), so each spoke runs as
// its own adapter. Its stored protocol value is "aave-v4:<spoke address>", which
// keeps every table key, the discovery watermark and the health cache per spoke
// with no schema change. The API splits it back into protocol "aave-v4" and
// market "<spoke>" (see SplitProtocol).
const aaveV4Prefix = string(ProtocolAaveV4) + ":"

// AaveV4Protocol returns the stored protocol value for one Aave V4 spoke.
func AaveV4Protocol(spoke string) Protocol {
	return Protocol(aaveV4Prefix + NormalizeAddr(spoke))
}

// IsAaveV4 reports whether p is a per-spoke Aave V4 protocol value.
func IsAaveV4(p Protocol) bool {
	return strings.HasPrefix(string(p), aaveV4Prefix)
}

// BaseProtocolSQL is the ClickHouse expression for the public protocol name of a
// stored protocol column ("aave-v4:<spoke>" -> "aave-v4", others unchanged).
const BaseProtocolSQL = "splitByChar(':', protocol)[1]"

// SplitProtocol splits a stored protocol value into the public protocol name and
// its market. Only Aave V4 carries a market (the spoke). Every other protocol
// returns its value unchanged and an empty market.
func SplitProtocol(p string) (protocol, market string) {
	if i := strings.IndexByte(p, ':'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return p, ""
}

// Side labels an asset leg of a position.
type Side string

const (
	SideCollateral Side = "collateral"
	SideBorrow     Side = "borrow"
	SideDebt       Side = "debt"
)

// Tier is the refresh cadence band a position currently sits in.
type Tier string

const (
	TierHot  Tier = "hot"
	TierWarm Tier = "warm"
	TierCold Tier = "cold"
)

// Address roles persisted in lending_protocol_addresses.
const (
	RolePool         = "pool"
	RoleOracle       = "oracle"
	RoleProvider     = "provider"
	RoleDataProvider = "data_provider"
	RoleComptroller  = "comptroller"
	RoleMarket       = "market"
	RoleMulticall    = "multicall"
	RoleSpoke        = "spoke"
)

// Multicall3 is deployed at the same address on every chain, including Avalanche.
const Multicall3Address = "0xcA11bde05977b3631167028862bE2a173976CA11"

// ZeroAddress is the 20-byte zero address, 0x-prefixed lowercase.
const ZeroAddress = "0x0000000000000000000000000000000000000000"

// Addresses are the resolved and on-chain-verified addresses for one deployment.
type Addresses struct {
	Pool         string   // Aave Pool
	Oracle       string   // Aave AaveOracle or Benqi PriceOracle
	Provider     string   // Aave PoolAddressesProvider
	DataProvider string   // Aave ProtocolDataProvider (AaveProtocolDataProvider)
	Comptroller  string   // Benqi Comptroller
	Markets      []string // Benqi qiToken markets
}

// Account identifies one tracked borrower on a protocol.
type Account struct {
	Protocol Protocol
	Address  string // 0x-prefixed lowercase
}

// Exposure is one (asset, side) membership for an account, used to bound health
// reads and to fan out price-triggered recomputes by asset (rule 1).
type Exposure struct {
	Account string
	Asset   string
	Side    Side
	Block   uint32
}

// AssetParam is a per-asset risk parameter set (Aave reserve or Benqi market).
type AssetParam struct {
	Asset                   string
	Market                  string // Benqi qiToken or Aave aToken, empty when not applicable
	Symbol                  string
	Decimals                uint8
	LiquidationThresholdBps uint16
	LiquidationBonusBps     uint16 // Aave per-reserve multiplier (10500 = 105%). 0 for Benqi (global)
	LtvBps                  uint16
	CanCollateral           bool
	CanBorrow               bool
}

// GlobalParams holds protocol-global parameters. Benqi close factor and
// liquidation incentive are Comptroller-global and belong here (rule 6).
type GlobalParams struct {
	CloseFactorBps          uint16   // Benqi global close factor (5000 = 50%). 0 for Aave (rule-based)
	LiquidationIncentiveBps uint16   // Benqi global incentive multiplier (10800 = 108%). 0 for Aave
	SmallPositionBase       *big.Int // Aave small-position close-factor threshold, base currency. nil/0 disables
	BaseCurrencyUnit        *big.Int // oracle base unit (Aave 1e8)
}

// AssetPosition is one asset leg of a user's position.
type AssetPosition struct {
	Asset     string
	Side      Side
	Amount    *big.Int // token units
	BaseValue *big.Int // oracle base-currency value
	// NotEntered marks Benqi supply in a market the account has not entered as
	// collateral. It backs no borrow, so it is excluded from collateral_base and the
	// health factor, but it is still seizable in a liquidation (Benqi seizeAllowed
	// checks only that both markets are listed). Zero value = entered, so Aave and
	// debt legs are unaffected.
	NotEntered bool
	// ConfigKey is the Aave V4 dynamic config key of a collateral leg. V4 collateral
	// factors are set per (reserve, key) and each user position pins a key, so the
	// key is needed to weight the leg correctly. 0 for every other protocol.
	ConfigKey uint32
}

// Health is the unified per-account health snapshot produced by an adapter.
type Health struct {
	Account        Account
	HealthFactor   *big.Int // 1e18-scaled. Benqi derived, ranking and display only (rule 3)
	CollateralBase *big.Int // weighted collateral in oracle base currency
	DebtBase       *big.Int
	ShortfallBase  *big.Int // Benqi shortfall, nil or 0 for Aave
	LiquidityBase  *big.Int // Benqi positive account liquidity (getAccountLiquidity word1), distance to liquidation; nil/0 for Aave
	Liquidatable   bool     // Aave HF<1e18, Benqi shortfall>0 (rule 3)
	EModeCategory  uint8    // Aave V3 getUserEMode, 0 when not in E-Mode (always 0 for Benqi)
	Assets         []AssetPosition
	BlockNumber    uint64
	OK             bool // false if the read reverted or failed, so it is skipped not zeroed (rule 5)
	// KeepLegs lists assets whose per-asset read failed (or could not be built)
	// while the summary succeeded. Their previous live legs are carried forward
	// rather than inferred closed, so a failed read is skipped, not zeroed (rule 5).
	KeepLegs []string
}
