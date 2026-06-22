package stealtime

import (
	"strings"
	"testing"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
)

func topicOf(addr string) string {
	return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(addr), "0x")
}

func dataWords(addrs ...string) []byte {
	var out []byte
	for _, a := range addrs {
		w := make([]byte, 32)
		copy(w[12:], lending.DecodeHexBytes(a))
		out = append(out, w...)
	}
	return out
}

func TestDecodeAaveLiquidation(t *testing.T) {
	coll := "0x1111111111111111111111111111111111111111"
	debt := "0x2222222222222222222222222222222222222222"
	user := "0x3333333333333333333333333333333333333333"
	liquidator := "0x4444444444444444444444444444444444444444"

	l := lending.LogRow{
		Topic0: topicAaveLiquidation,
		Topic1: topicOf(coll),
		Topic2: topicOf(debt),
		Topic3: topicOf(user),
		// data: debtToCover, liquidatedCollateralAmount, liquidator, receiveAToken
		Data:  dataWords("0x64", "0xc8", liquidator, "0x0"),
		Block: 5000,
	}
	got, ok := decodeAave(l)
	if !ok {
		t.Fatal("decode failed")
	}
	if got.Account != common.HexToAddress(user) || got.CollateralAsset != common.HexToAddress(coll) ||
		got.DebtAsset != common.HexToAddress(debt) || got.Liquidator != common.HexToAddress(liquidator) {
		t.Fatalf("decoded wrong: %+v", got)
	}
	if got.TakenBlock != 5000 || got.Protocol != "aave-v3" {
		t.Fatalf("meta wrong: %+v", got)
	}
}

func TestDecodeBenqiLiquidation(t *testing.T) {
	market := "0x5C0401e81Bc07Ca70fAD469b451682c0d747Ef1c" // qiAVAX (the borrowed market, log address)
	liquidator := "0x4444444444444444444444444444444444444444"
	borrower := "0x3333333333333333333333333333333333333333"
	collMarket := "0x6666666666666666666666666666666666666666"

	l := lending.LogRow{
		Address: market,
		Topic0:  topicBenqiLiquidation,
		// data: liquidator, borrower, repayAmount, cTokenCollateral, seizeTokens
		Data:  dataWords(liquidator, borrower, "0x64", collMarket, "0xc8"),
		Block: 6000,
	}
	got, ok := decodeBenqi(l)
	if !ok {
		t.Fatal("decode failed")
	}
	if got.Account != common.HexToAddress(borrower) || got.Liquidator != common.HexToAddress(liquidator) ||
		got.DebtAsset != common.HexToAddress(market) || got.CollateralAsset != common.HexToAddress(collMarket) {
		t.Fatalf("decoded wrong: %+v", got)
	}
	if got.TakenBlock != 6000 || got.Protocol != "benqi" {
		t.Fatalf("meta wrong: %+v", got)
	}
}
