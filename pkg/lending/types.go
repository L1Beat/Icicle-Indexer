// Package lending implements a real-time lending-position health feed for
// Avalanche C-Chain. It discovers borrow positions from event logs the indexer
// already stores in raw_logs, reads health directly from the protocols through
// Multicall3 on a tiered schedule, and exposes a ranked liquidation-risk view.
//
// This package builds the data feed only. Liquidation execution, flash loans,
// collateral-swap routing, and transaction submission are out of scope.
package lending

import "math/big"

// Protocol identifies a supported lending market.
type Protocol string

const (
	ProtocolAaveV3 Protocol = "aave-v3"
	ProtocolBenqi  Protocol = "benqi"
)

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
}

// Health is the unified per-account health snapshot produced by an adapter.
type Health struct {
	Account        Account
	HealthFactor   *big.Int // 1e18-scaled. Benqi derived, ranking and display only (rule 3)
	CollateralBase *big.Int // weighted collateral in oracle base currency
	DebtBase       *big.Int
	ShortfallBase  *big.Int // Benqi shortfall, nil or 0 for Aave
	Liquidatable   bool     // Aave HF<1e18, Benqi shortfall>0 (rule 3)
	Assets         []AssetPosition
	BlockNumber    uint64
	OK             bool // false if the read reverted or failed, so it is skipped not zeroed (rule 5)
}
