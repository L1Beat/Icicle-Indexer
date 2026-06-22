package stealtime

import (
	"math/big"
	"testing"

	"github.com/ava-labs/libevm/common"
)

func usd(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), wad) }

func TestAggregate(t *testing.T) {
	liqA := common.HexToAddress("0xaaaa")
	liqB := common.HexToAddress("0xbbbb")

	obs := []Observation{
		{Liquidator: liqA, Protocol: "aave-v3", StealTime: 0, NetProfitUSD: usd(500), SizeBucket: "medium"},
		{Liquidator: liqA, Protocol: "aave-v3", StealTime: 1, NetProfitUSD: usd(2000), SizeBucket: "large"},
		{Liquidator: liqA, Protocol: "benqi", StealTime: 2, NetProfitUSD: usd(50), SizeBucket: "small"},
		{Liquidator: liqB, Protocol: "benqi", StealTime: 15, NetProfitUSD: usd(300), SizeBucket: "medium"},
		{Liquidator: liqB, Protocol: "benqi", StealTime: 0, Censored: true, NetProfitUSD: usd(40), SizeBucket: "small"},
	}

	d := Aggregate(obs, 1)

	if d.Total != 5 || d.Censored != 1 {
		t.Fatalf("total=%d censored=%d", d.Total, d.Censored)
	}
	if d.Overall.B0 != 1 || d.Overall.B1 != 1 || d.Overall.B2 != 1 || d.Overall.B11to20 != 1 || d.Overall.Censored != 1 {
		t.Fatalf("histogram wrong: %+v", d.Overall)
	}
	// Non-censored steals: [0,1,2,15]. Median index = 50*3/100 = 1 -> 1. p90 index = 90*3/100 = 2 -> 2.
	if d.MedianBlocks != 1 {
		t.Errorf("median: got %d want 1", d.MedianBlocks)
	}
	if d.P90Blocks != 2 {
		t.Errorf("p90: got %d want 2", d.P90Blocks)
	}
	// Within two: 0,1,2 -> 3 of 4 = 0.75. Beyond ten: 15 -> 1 of 4 = 0.25.
	if d.WithinTwo != 0.75 || d.BeyondTen != 0.25 {
		t.Errorf("within=%v beyond=%v", d.WithinTwo, d.BeyondTen)
	}
	// Total profit: 500+2000+50+300+40 = 2890.
	if d.TotalProfit.Cmp(usd(2890)) != 0 {
		t.Errorf("total profit: got %s", d.TotalProfit)
	}
	// Top 1 liquidator is liqA with 3 of 5 = 0.6.
	if len(d.TopLiquidators) != 1 || d.TopLiquidators[0].Liquidator != liqA || d.TopLiquidators[0].Count != 3 {
		t.Errorf("top liquidators wrong: %+v", d.TopLiquidators)
	}
	if d.TopNShare != 0.6 {
		t.Errorf("topN share: got %v want 0.6", d.TopNShare)
	}
}

func TestSizeBucketFor(t *testing.T) {
	if SizeBucketFor(usd(5)) != "small" {
		t.Error("5 -> small")
	}
	if SizeBucketFor(usd(100)) != "medium" {
		t.Error("100 -> medium")
	}
	if SizeBucketFor(usd(1000)) != "large" {
		t.Error("1000 -> large")
	}
}
