package kmeasure

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
)

// stubReader is a programmable EthReader for resolver and quoter tests.
type stubReader struct {
	calls int
	fn    func(to, data string) (string, error)
}

func (s *stubReader) EthCall(_ context.Context, to, data, _ string) (string, error) {
	s.calls++
	return s.fn(to, data)
}
func (s *stubReader) BlockNumber(context.Context) (uint64, error) { return 0, nil }
func (s *stubReader) GasPrice(context.Context) (*big.Int, error)  { return big.NewInt(0), nil }

func encodeUintArray(vals ...*big.Int) string {
	var b []byte
	b = append(b, word(big.NewInt(0x20))...)
	b = append(b, word(big.NewInt(int64(len(vals))))...)
	for _, v := range vals {
		b = append(b, word(v)...)
	}
	return "0x" + hex.EncodeToString(b)
}

func encodeUintWord(v *big.Int) string { return "0x" + hex.EncodeToString(word(v)) }

// --- RichQuoter stub for the adapter test ---

type stubRich struct {
	out   *big.Int
	route Route
	err   error
}

func (s stubRich) QuoteRoute(context.Context, common.Address, uint8, common.Address, uint8, *big.Int) (*big.Int, Route, error) {
	return s.out, s.route, s.err
}

func TestQuoterAdapterMapsFailureToZero(t *testing.T) {
	in := common.HexToAddress("0x01")
	out := common.HexToAddress("0x02")
	amt := big.NewInt(1000)

	// Success passes through.
	a := NewQuoterAdapter(stubRich{out: big.NewInt(950)})
	got, err := a.QuoteOut(context.Background(), in, 18, out, 6, amt)
	if err != nil || got.Cmp(big.NewInt(950)) != 0 {
		t.Fatalf("success: got %v err %v", got, err)
	}

	// Error maps to zero, never an error (no crash, classified illiquid).
	a = NewQuoterAdapter(stubRich{err: errors.New("revert")})
	got, err = a.QuoteOut(context.Background(), in, 18, out, 6, amt)
	if err != nil || got.Sign() != 0 {
		t.Fatalf("error case: got %v err %v", got, err)
	}
	if calls, failures, _ := a.Stats(); calls != 1 || failures != 1 {
		t.Fatalf("stats: calls=%d failures=%d", calls, failures)
	}

	// No route (zero output) also counts as a failure and returns zero.
	a = NewQuoterAdapter(stubRich{out: big.NewInt(0)})
	got, err = a.QuoteOut(context.Background(), in, 18, out, 6, amt)
	if err != nil || got.Sign() != 0 {
		t.Fatalf("zero out: got %v err %v", got, err)
	}
	if _, failures, _ := a.Stats(); failures != 1 {
		t.Fatalf("zero out should count as failure, got %d", failures)
	}
}

func TestDexQuoterPicksBestRoute(t *testing.T) {
	tokenIn := common.HexToAddress("0x0000000000000000000000000000000000000011")
	tokenOut := common.HexToAddress("0x0000000000000000000000000000000000000022")
	router1 := common.HexToAddress("0x00000000000000000000000000000000000000f1")
	router2 := common.HexToAddress("0x00000000000000000000000000000000000000f2")
	amountIn := big.NewInt(1_000_000)

	reader := &stubReader{fn: func(to, _ string) (string, error) {
		switch common.HexToAddress(to) {
		case router1:
			// getAmountsOut returns [amountIn, 990000]
			return encodeUintArray(amountIn, big.NewInt(990_000)), nil
		case router2:
			return "", errors.New("no pair")
		default:
			return "", errors.New("unexpected target")
		}
	}}

	q := NewDexQuoter(reader, []common.Address{router1, router2})
	out, route, err := q.QuoteRoute(context.Background(), tokenIn, 18, tokenOut, 6, amountIn)
	if err != nil {
		t.Fatalf("QuoteRoute: %v", err)
	}
	if out.Cmp(big.NewInt(990_000)) != 0 {
		t.Fatalf("best out: got %s want 990000", out)
	}
	if route.Router != router1 {
		t.Fatalf("route router: got %s want %s", route.Router.Hex(), router1.Hex())
	}
}

func TestResolverMapsNativeBenqiToWavax(t *testing.T) {
	qiAVAX := common.HexToAddress("0x5C0401e81Bc07Ca70fAD469b451682c0d747Ef1c")
	underlyingSel := lending.Selector("underlying()")
	decimalsSel := lending.Selector("decimals()")

	reader := &stubReader{fn: func(_, data string) (string, error) {
		switch {
		case has(data, underlyingSel):
			return "", errors.New("execution reverted") // native market, no underlying
		case has(data, decimalsSel):
			return encodeUintWord(big.NewInt(18)), nil
		default:
			return "", errors.New("unexpected call")
		}
	}}

	r := NewChainResolver(reader)
	info, err := r.Resolve(context.Background(), "benqi", qiAVAX)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info.Underlying != WAVAX || info.Decimals != 18 {
		t.Fatalf("got underlying=%s dec=%d want WAVAX/18", info.Underlying.Hex(), info.Decimals)
	}

	// Second call is cached: no new EthCalls.
	before := reader.calls
	if _, err := r.Resolve(context.Background(), "benqi", qiAVAX); err != nil {
		t.Fatalf("Resolve cached: %v", err)
	}
	if reader.calls != before {
		t.Fatalf("expected cache hit, calls went %d -> %d", before, reader.calls)
	}
}

func has(data, selector string) bool {
	return len(data) >= len(selector) && data[:len(selector)] == selector
}
