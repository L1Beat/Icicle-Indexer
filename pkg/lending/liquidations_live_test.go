package lending

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestLiquidationValuation_Live values the most recent real liquidation of each
// protocol through the ingester's valuation paths. Set LENDING_LIVE_RPC.
func TestLiquidationValuation_Live(t *testing.T) {
	url := os.Getenv("LENDING_LIVE_RPC")
	if url == "" {
		t.Skip("LENDING_LIVE_RPC not set")
	}
	ctx := context.Background()
	rpc := NewClient(url, "")
	li := NewLiquidationIngester(nil, rpc, 43114, nil)

	head, err := rpc.BlockNumber(ctx)
	if err != nil {
		t.Fatal(err)
	}

	type rawLog struct {
		Address     string   `json:"address"`
		Topics      []string `json:"topics"`
		Data        string   `json:"data"`
		BlockNumber string   `json:"blockNumber"`
	}
	getLogs := func(addrs []string, topic string, span uint64) []rawLog {
		var out []rawLog
		for from := head - span; from < head; from += 20000 {
			body, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "eth_getLogs",
				"params": []interface{}{map[string]interface{}{"address": addrs, "topics": []string{topic},
					"fromBlock": "0x" + strconv.FormatUint(from, 16), "toBlock": "0x" + strconv.FormatUint(from+19999, 16)}}})
			resp, err := http.Post(url, "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			var r struct{ Result []rawLog }
			_ = json.NewDecoder(resp.Body).Decode(&r)
			resp.Body.Close()
			out = append(out, r.Result...)
		}
		return out
	}
	b32 := func(h string) (o [32]byte) {
		b, _ := hex.DecodeString(strings.TrimPrefix(h, "0x"))
		copy(o[:], b)
		return
	}
	usd := func(v *big.Int) float64 {
		f, _ := new(big.Float).Quo(new(big.Float).SetInt(v), big.NewFloat(1e18)).Float64()
		return f
	}

	// Aave V4
	v4 := getLogs(aaveV4Spokes, topicV4Liquidation, 6_000_000)
	if len(v4) == 0 {
		t.Fatal("no v4 liquidations found")
	}
	l := v4[len(v4)-1]
	data, _ := hex.DecodeString(strings.TrimPrefix(l.Data, "0x"))
	blk, _ := strconv.ParseUint(strings.TrimPrefix(l.BlockNumber, "0x"), 16, 64)
	t1, t2 := b32(l.Topics[1]), b32(l.Topics[2])
	coll, ok1 := li.v4ReserveAt(ctx, strings.ToLower(l.Address), new(big.Int).SetBytes(t1[:]))
	debt, ok2 := li.v4ReserveAt(ctx, strings.ToLower(l.Address), new(big.Int).SetBytes(t2[:]))
	if !ok1 || !ok2 {
		t.Fatal("v4 reserves unresolved")
	}
	r := LiquidationRow{Block: uint32(blk), RepayAmount: new(big.Int).Add(Word(data, 2), new(big.Int).Div(Word(data, 6), ray)), SeizeAmount: Word(data, 7)}
	rep, sei := li.valueV4(ctx, strings.ToLower(l.Address), new(big.Int).SetBytes(t2[:]), new(big.Int).SetBytes(t1[:]), debt, coll, r)
	t.Logf("v4 %d liquidations; last: debt %x (dec %d) repay %s = $%.2f, coll %x seize %s = $%.2f",
		len(v4), debt.underlying, debt.decimals, r.RepayAmount, usd(rep), coll.underlying, r.SeizeAmount, usd(sei))
	if rep.Sign() == 0 || sei.Sign() == 0 || usd(sei) < usd(rep) {
		t.Errorf("v4 valuation looks wrong: repaid %v seized %v", usd(rep), usd(sei))
	}

	// Benqi: qiToken markets from the comptroller.
	benqi := liqSource{protocol: "benqi", isBenqi: true, anchor: "0x486af39519b4dc9a7fccd318217352830e8ad9b4", oracleSig: "oracle()"}
	res, err := rpc.EthCall(ctx, benqi.anchor, EncodeCall0("getAllMarkets()"), "latest")
	if err != nil {
		t.Fatal(err)
	}
	mb := DecodeHexBytes(res)
	n := int(Word(mb, 1).Int64())
	var markets []string
	for i := 0; i < n; i++ {
		markets = append(markets, Addr(mb, 2+i))
	}
	bq := getLogs(markets, topicBenqiLiquidation, 3_000_000)
	if len(bq) > 0 {
		l := bq[len(bq)-1]
		data, _ := hex.DecodeString(strings.TrimPrefix(l.Data, "0x"))
		blk, _ := strconv.ParseUint(strings.TrimPrefix(l.BlockNumber, "0x"), 16, 64)
		row := LiquidationRow{Block: uint32(blk), RepayAmount: Word(data, 2), SeizeAmount: Word(data, 4), Collateral: dataAddr(data, 3)}
		copy(row.Debt[:], DecodeHexBytes(l.Address))
		rep := li.valueRepaid(ctx, benqi, row)
		und, sei := li.valueSeized(ctx, benqi, row)
		t.Logf("benqi %d liquidations; last: repaid $%.2f, seized %s qi -> %s underlying = $%.2f", len(bq), usd(rep), row.SeizeAmount, und, usd(sei))
		if sei.Sign() == 0 || usd(sei) < usd(rep) {
			t.Errorf("benqi valuation looks wrong: repaid %v seized %v", usd(rep), usd(sei))
		}
	}

	// Aave V3
	aave := liqSource{protocol: "aave-v3", anchor: "0xa97684ead0e402dc232d5a977953df7ecbab3cdb", oracleSig: "getPriceOracle()",
		decimals: map[string]uint8{"0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7": 18, "0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e": 6,
			"0x9702230a8ea53601f5cd2dc00fdbc13d4df4a8c7": 6, "0x2b2c81e08f1af8835a78bb2a90ae924ace0ea4be": 18, "0x152b9d0fdc40c096757f570a51e494bd4b943e50": 8,
			"0x49d5c2bdffac6ce2bfdb6640f4f80f226bc10bab": 18}}
	av := getLogs([]string{"0x794a61358d6845594f94dc1db02a252b5b4814ad"}, topicAaveLiquidation, 2_000_000)
	if len(av) > 0 {
		l := av[len(av)-1]
		data, _ := hex.DecodeString(strings.TrimPrefix(l.Data, "0x"))
		blk, _ := strconv.ParseUint(strings.TrimPrefix(l.BlockNumber, "0x"), 16, 64)
		row := LiquidationRow{Block: uint32(blk), RepayAmount: Word(data, 0), SeizeAmount: Word(data, 1), Collateral: last20(b32(l.Topics[1])), Debt: last20(b32(l.Topics[2]))}
		rep := li.valueRepaid(ctx, aave, row)
		_, sei := li.valueSeized(ctx, aave, row)
		t.Logf("aave-v3 %d liquidations; last: coll %x debt %x repaid $%.2f seized $%.2f", len(av), row.Collateral, row.Debt, usd(rep), usd(sei))
	}
}
