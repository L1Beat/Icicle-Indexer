package lendingmarkets

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
)

// fakeHistory is a History over scripted state: block n has time base+2n, and
// state functions answer calls by selector.
type fakeHistory struct {
	base  time.Time
	calls map[string]func(block uint64, args []byte) ([]byte, error) // "to|sig"
	logs  map[uint64]string                                          // block -> tx
}

func (f *fakeHistory) CallAt(_ context.Context, block uint64, to string, data []byte) ([]byte, error) {
	for k, fn := range f.calls {
		addr, sig, _ := strings.Cut(k, "|")
		if strings.EqualFold(addr, to) && bytes.Equal(data[:4], selector(sig)) {
			return fn(block, data[4:])
		}
	}
	if bytes.Equal(data[:4], selector("getReserveCount()")) {
		return words(0), nil // other spokes: deployed, no reserves
	}
	return nil, nil // no code: empty return
}

func (f *fakeHistory) BlockTime(_ context.Context, block uint64) (time.Time, error) {
	return f.base.Add(time.Duration(block) * 2 * time.Second), nil
}

func (f *fakeHistory) LogTx(_ context.Context, block uint64, _ string, _ [][]string) (string, error) {
	return f.logs[block], nil
}

func words(vs ...uint64) []byte {
	var out []byte
	for _, v := range vs {
		out = append(out, uintWord(v)...)
	}
	return out
}

func TestDateAaveV4Spokes_RestoredValue(t *testing.T) {
	spoke := AaveV4Spokes[0]
	asset := "0x00000000000000000000000000000000000000aa"
	// Key 0 (cf 7300) from block 10, key 1 (cf 0) from block 40, key 2 (cf
	// 7300 again) from block 70. The current 7300 dates to block 70, not 10.
	keyAt := func(b uint64) uint64 {
		switch {
		case b >= 70:
			return 2
		case b >= 40:
			return 1
		}
		return 0
	}
	cfs := []uint64{7300, 0, 7300}
	f := &fakeHistory{base: time.Unix(1_700_000_000, 0).UTC(), logs: map[uint64]string{70: "0xtx70"}}
	f.calls = map[string]func(uint64, []byte) ([]byte, error){
		spoke + "|getReserveCount()": func(b uint64, _ []byte) ([]byte, error) {
			if b < 10 {
				return words(0), nil
			}
			return words(1), nil
		},
		spoke + "|getReserve(uint256)": func(b uint64, _ []byte) ([]byte, error) {
			if b < 10 {
				return nil, errors.New("execution reverted")
			}
			w := append(addrWord(asset), words(0, 0, 18, 0, 0, keyAt(b))...)
			return w, nil
		},
		spoke + "|getDynamicReserveConfig(uint256,uint32)": func(b uint64, args []byte) ([]byte, error) {
			k := new(big.Int).SetBytes(args[32:64]).Uint64()
			return words(cfs[k], 10500, 1000), nil
		},
	}
	chs, err := DateAaveV4Spokes(context.Background(), f, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(chs) != 1 {
		t.Fatalf("got %d changes, want 1", len(chs))
	}
	c := chs[0]
	if c.LTVBps != 7300 || c.Block != 70 || c.Tx != "0xtx70" || c.GroupID != strings.ToLower(spoke) {
		t.Fatalf("got %+v", c)
	}
	if want := f.base.Add(140 * time.Second); !c.At.Equal(want) {
		t.Fatalf("at %v, want %v", c.At, want)
	}

	// Already dated at the same value: skipped.
	chs, err = DateAaveV4Spokes(context.Background(), f, 100, func(string, string, uint16) bool { return true })
	if err != nil || len(chs) != 0 {
		t.Fatalf("skip: %v %v", chs, err)
	}

	// Unchanged since the first config: no date.
	cfs = []uint64{7300, 7300, 7300}
	chs, err = DateAaveV4Spokes(context.Background(), f, 100, nil)
	if err != nil || len(chs) != 1 || chs[0].At != nil || chs[0].Block != 0 {
		t.Fatalf("unchanged: %+v %v", chs, err)
	}
}

func TestDateMarketLTV_Benqi(t *testing.T) {
	qi := "0x00000000000000000000000000000000000000bb"
	// Collateral factor 0.75 until block 55, then 0.
	f := &fakeHistory{base: time.Unix(1_700_000_000, 0).UTC(), logs: map[uint64]string{55: "0xtx55"}}
	f.calls = map[string]func(uint64, []byte) ([]byte, error){
		BenqiComptroller + "|markets(address)": func(b uint64, _ []byte) ([]byte, error) {
			cf := new(big.Int).Mul(big.NewInt(75), big.NewInt(1e16))
			if b >= 55 {
				cf = new(big.Int)
			}
			return append(words(1), cf.FillBytes(make([]byte, 32))...), nil
		},
	}
	ctx := context.Background()
	lo, err := BlockAtTime(ctx, f, f.base.Add(41*time.Second), 100) // between blocks 20 and 21
	if err != nil || lo != 20 {
		t.Fatalf("BlockAtTime: %d %v", lo, err)
	}
	c, err := DateMarketLTV(ctx, f, ProtocolBenqi, qi, NativeAVAX, 0, lo, 90)
	if err != nil {
		t.Fatal(err)
	}
	if c.Block != 55 || c.Tx != "0xtx55" || c.At == nil {
		t.Fatalf("got %+v", c)
	}
	// A window that does not hold the change is an error, not a wrong date.
	if _, err := DateMarketLTV(ctx, f, ProtocolBenqi, qi, NativeAVAX, 0, 60, 90); err == nil {
		t.Fatal("want error when the window starts after the change")
	}
	if _, err := DateMarketLTV(ctx, f, ProtocolBenqi, qi, NativeAVAX, 7500, 10, 90); err == nil {
		t.Fatal("want error when the window ends before the value")
	}
}
