package lending

import "context"

// LogRow is a decoded raw_logs row handed to an adapter for position discovery.
// Topics are 0x-prefixed 32-byte hex, empty when absent. Data is the raw,
// non-indexed event payload.
type LogRow struct {
	Address string
	Topic0  string
	Topic1  string
	Topic2  string
	Topic3  string
	Data    []byte
	Block   uint32
}

// DiscoverySpec tells the discovery loop which contracts and event topics to scan
// in raw_logs for a protocol.
type DiscoverySpec struct {
	Addresses []string // contract addresses whose logs are relevant
	Topics    []string // topic0 hashes of the events we decode
}

// VerifyNote records the outcome of one on-chain address check, so resolution can
// log a loud warning when a resolved value differs from what was expected.
type VerifyNote struct {
	Role     string
	Expected string // empty when there is no canonical expectation
	Resolved string
	OK       bool
	Detail   string
}

// HealthProbe is the set of Multicall3 calls needed to read one account's health,
// plus a decoder that turns the matching results back into a Health. The engine
// batches Calls from many probes into aggregate3 requests and then feeds each
// probe its own slice of results, in order.
type HealthProbe struct {
	Account string
	Calls   []Call
	Decode  func(results []CallResult, blockNumber uint64) Health
}

// Adapter is the per-protocol contract. Adding a protocol means implementing this
// once: the discovery, health-engine, and feed layers never branch on protocol.
type Adapter interface {
	// Protocol returns the protocol identifier.
	Protocol() Protocol

	// Resolve reads and verifies the protocol's addresses on-chain. It returns the
	// resolved set and a per-check report. An error means the protocol could not be
	// brought up and should be skipped.
	Resolve(ctx context.Context, rpc *Client) (Addresses, []VerifyNote, error)

	// Configure injects resolved addresses and refreshed parameters before the
	// adapter builds probes or decodes logs.
	Configure(addrs Addresses, params []AssetParam, globals GlobalParams)

	// Discovery returns the contracts and topics to scan in raw_logs.
	Discovery() DiscoverySpec

	// DecodeLog turns one raw_logs row into account exposures. An exposure with an
	// empty Asset records only that the account was touched and should be tracked.
	DecodeLog(l LogRow) []Exposure

	// BuildProbe builds the health probe for one account. When exposure is empty
	// the probe is summary-only (cheap, for the cold sweep). When exposure is
	// provided the probe also reads per-asset detail for the feed.
	BuildProbe(account string, exposure []Exposure) HealthProbe

	// RefreshParams reads per-asset and protocol-global risk parameters on-chain.
	RefreshParams(ctx context.Context, rpc *Client) ([]AssetParam, GlobalParams, error)
}
