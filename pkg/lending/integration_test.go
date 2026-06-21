package lending_test

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"icicle/pkg/lending"
	"icicle/pkg/lending/aave"
)

// TestPreLiquidationDetection asserts the engine's own read path would have
// flagged a known historical position as liquidatable in the block before its
// Aave v3 LiquidationCall, not merely that the liquidation can be parsed after
// the fact (rule 7).
//
// It is gated on a live archive node and a known liquidation fixture, supplied by
// environment so no possibly-wrong address is baked in:
//
//	LENDING_IT_ARCHIVE_RPC  archive node RPC URL (archive state, debug not required)
//	LENDING_IT_ACCOUNT      the liquidated borrower address
//	LENDING_IT_LIQ_BLOCK    the block number of the LiquidationCall
//
//	Run: LENDING_IT_ARCHIVE_RPC=... LENDING_IT_ACCOUNT=0x... LENDING_IT_LIQ_BLOCK=... \
//		go test ./pkg/lending/ -run TestPreLiquidationDetection -v
func TestPreLiquidationDetection(t *testing.T) {
	rpcURL := os.Getenv("LENDING_IT_ARCHIVE_RPC")
	account := os.Getenv("LENDING_IT_ACCOUNT")
	liqBlockStr := os.Getenv("LENDING_IT_LIQ_BLOCK")
	if rpcURL == "" || account == "" || liqBlockStr == "" {
		t.Skip("set LENDING_IT_ARCHIVE_RPC, LENDING_IT_ACCOUNT and LENDING_IT_LIQ_BLOCK to run")
	}
	liqBlock, err := strconv.ParseUint(liqBlockStr, 10, 64)
	if err != nil {
		t.Fatalf("LENDING_IT_LIQ_BLOCK: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rpc := lending.NewClient(rpcURL, "")
	adapter := aave.New("")

	addrs, _, err := adapter.Resolve(ctx, rpc)
	if err != nil {
		t.Fatalf("resolve aave: %v", err)
	}

	// Read getUserAccountData at the block immediately before the liquidation,
	// the same call and decoding the engine uses, but pinned to a historical block.
	preBlock := "0x" + strconv.FormatUint(liqBlock-1, 16)
	res, err := rpc.EthCall(ctx, addrs.Pool, lending.EncodeCall1Addr("getUserAccountData(address)", account), preBlock)
	if err != nil {
		t.Fatalf("getUserAccountData at %s: %v", preBlock, err)
	}
	data := lending.DecodeHexBytes(res)

	debt := lending.Word(data, 1)
	hf := lending.Word(data, 5)
	if debt.Sign() == 0 {
		t.Fatalf("account had no debt at block %d, check the fixture", liqBlock-1)
	}

	liquidatable := hf.Cmp(lending.WAD) < 0
	t.Logf("pre-liquidation block %d: healthFactor=%s debtBase=%s liquidatable=%v",
		liqBlock-1, hf.String(), debt.String(), liquidatable)

	if !liquidatable {
		t.Fatalf("expected health factor < 1e18 before liquidation, got %s", hf.String())
	}
}
