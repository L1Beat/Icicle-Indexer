package kmeasure

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/prefilter"
)

var (
	wavaxAddr  = WAVAX
	usdcAddr   = common.HexToAddress("0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E")
	qiAVAXAddr = common.HexToAddress("0x5C0401e81Bc07Ca70fAD469b451682c0d747Ef1c")
	qiUSDCAddr = common.HexToAddress("0xB715808a78F6041E46d61Cb123C9B4A27056AE9C")
)

// stubResolver maps the test qiTokens to underlyings and gives Aave assets their decimals.
type stubResolver struct{}

func (stubResolver) Resolve(_ context.Context, protocol string, asset common.Address) (TokenInfo, error) {
	switch asset {
	case qiAVAXAddr:
		return TokenInfo{Underlying: wavaxAddr, Decimals: 18}, nil
	case qiUSDCAddr:
		return TokenInfo{Underlying: usdcAddr, Decimals: 6}, nil
	case usdcAddr:
		return TokenInfo{Underlying: usdcAddr, Decimals: 6}, nil
	default:
		return TokenInfo{Underlying: asset, Decimals: 18}, nil
	}
}

const feedFixture = `{
  "data": [
    {
      "account": "0x00000000000000000000000000000000000000a1",
      "protocol": "aave-v3",
      "health_factor": "1010000000000000000",
      "liquidatable": true,
      "collateral_base": "15000000000000000000000",
      "debt_base": "10000000000000000000000",
      "tier": "hot",
      "collateral": [{"asset": "0xB31f66AA3C1e785363F0875A1B74E27b85FD66c7", "amount": "600000000000000000000", "base_value": "15000000000000000000000"}],
      "debt": [{"asset": "0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E", "amount": "10000000000", "base_value": "10000000000000000000000"}],
      "block_number": 88531134
    },
    {
      "account": "0x00000000000000000000000000000000000000b2",
      "protocol": "benqi",
      "health_factor": "0",
      "liquidatable": true,
      "collateral_base": "0",
      "debt_base": "25830185522267148110",
      "shortfall_base": "25830185522267148110",
      "tier": "hot",
      "collateral": null,
      "debt": [{"asset": "0xB715808a78F6041E46d61Cb123C9B4A27056AE9C", "amount": "25", "base_value": "25830185522267148110"}],
      "block_number": 88531134
    },
    {
      "account": "0x00000000000000000000000000000000000000c3",
      "protocol": "benqi",
      "health_factor": "1001000000000000000",
      "liquidatable": false,
      "collateral_base": "21853000000000000000000",
      "debt_base": "17470000000000000000000",
      "tier": "hot",
      "collateral": [{"asset": "0x5C0401e81Bc07Ca70fAD469b451682c0d747Ef1c", "amount": "900000000000000000000", "base_value": "21853000000000000000000"}],
      "debt": [{"asset": "0xB715808a78F6041E46d61Cb123C9B4A27056AE9C", "amount": "17470000000", "base_value": "17470000000000000000000"}],
      "block_number": 88531134
    }
  ],
  "meta": {"limit": 100, "offset": 0, "has_more": false}
}`

func TestBuildPositionsFromFeedJSON(t *testing.T) {
	var env feedEnvelope
	if err := json.Unmarshal([]byte(feedFixture), &env); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(env.Data) != 3 {
		t.Fatalf("expected 3 positions, got %d", len(env.Data))
	}

	positions, err := BuildPositions(context.Background(), env.Data, stubResolver{})
	if err != nil {
		t.Fatalf("BuildPositions: %v", err)
	}

	// Position 1: Aave, underlying assets pass through, decimals resolved.
	p1 := positions[0]
	if got := countSide(p1, prefilter.SideCollateral); got != 1 {
		t.Errorf("p1 collateral legs: got %d want 1", got)
	}
	debt1 := legBySide(p1, prefilter.SideDebt)
	if debt1.Asset != usdcAddr || debt1.Decimals != 6 {
		t.Errorf("p1 debt leg: got %s dec=%d", debt1.Asset.Hex(), debt1.Decimals)
	}
	if debt1.Amount.String() != "10000000000" {
		t.Errorf("p1 debt amount decode: got %s", debt1.Amount)
	}

	// Position 2: zero-collateral bad debt. No collateral legs -> pre-filter no_pair.
	p2 := positions[1]
	if got := countSide(p2, prefilter.SideCollateral); got != 0 {
		t.Errorf("p2 should have 0 collateral legs (bad debt), got %d", got)
	}
	if got := countSide(p2, prefilter.SideDebt); got != 1 {
		t.Errorf("p2 debt legs: got %d want 1", got)
	}

	// Position 3: Benqi qiToken legs resolve to underlyings (qiAVAX -> WAVAX).
	p3 := positions[2]
	coll3 := legBySide(p3, prefilter.SideCollateral)
	if coll3.Asset != wavaxAddr || coll3.Decimals != 18 {
		t.Errorf("p3 collateral should resolve qiAVAX to WAVAX/18, got %s dec=%d", coll3.Asset.Hex(), coll3.Decimals)
	}
	debt3 := legBySide(p3, prefilter.SideDebt)
	if debt3.Asset != usdcAddr {
		t.Errorf("p3 debt should resolve qiUSDC to USDC, got %s", debt3.Asset.Hex())
	}
}

func countSide(p prefilter.Position, side prefilter.Side) int {
	n := 0
	for _, l := range p.Legs {
		if l.Side == side {
			n++
		}
	}
	return n
}

func legBySide(p prefilter.Position, side prefilter.Side) prefilter.AssetLeg {
	for _, l := range p.Legs {
		if l.Side == side {
			return l
		}
	}
	return prefilter.AssetLeg{}
}
