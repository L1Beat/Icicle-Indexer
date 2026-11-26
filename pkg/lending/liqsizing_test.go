package lending

import (
	"math/big"
	"testing"

	"github.com/ava-labs/libevm/common"
)

func TestLiquidationPremium(t *testing.T) {
	for in, want := range map[uint64]uint64{0: 0, 10000: 0, 10500: 500, 11000: 1000} {
		if got := LiquidationPremium(in); got != want {
			t.Errorf("LiquidationPremium(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestCloseFactorBps(t *testing.T) {
	low := new(big.Int).Div(WAD, big.NewInt(2))
	cases := []struct {
		protocol string
		hf       *big.Int
		benqi    uint64
		want     uint64
	}{
		{"benqi", low, 0, 5000},
		{"benqi", low, 6000, 6000},
		{"aave-v3", low, 0, 10000},
		{"aave-v3", hf095, 0, 5000},
		{"aave-v3", nil, 0, 5000},
	}
	for _, c := range cases {
		if got := CloseFactorBps(c.protocol, c.hf, c.benqi); got != c.want {
			t.Errorf("CloseFactorBps(%s, %v, %d) = %d, want %d", c.protocol, c.hf, c.benqi, got, c.want)
		}
	}
}

func TestEncodeGetAmountsOut(t *testing.T) {
	a := common.HexToAddress("0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7")
	b := common.HexToAddress("0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e")
	got := EncodeGetAmountsOut(big.NewInt(1000), []common.Address{a, b})
	want := Selector("getAmountsOut(uint256,address[])") +
		"00000000000000000000000000000000000000000000000000000000000003e8" +
		"0000000000000000000000000000000000000000000000000000000000000040" +
		"0000000000000000000000000000000000000000000000000000000000000002" +
		"000000000000000000000000b31f66aa3c1e785363f0875a1b74e27b85fd66c7" +
		"000000000000000000000000b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"
	if got != want {
		t.Errorf("EncodeGetAmountsOut =\n%s\nwant\n%s", got, want)
	}
}
