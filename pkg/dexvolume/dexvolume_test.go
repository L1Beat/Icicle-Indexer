package dexvolume

import (
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFactoriesMapToDexes(t *testing.T) {
	ids := map[string]bool{}
	for _, d := range Dexes {
		ids[d.ID] = true
	}
	seen := map[string]bool{}
	for _, f := range Factories {
		require.True(t, ids[f.Dex], f.Address)
		require.Equal(t, strings.ToLower(f.Address), f.Address)
		require.Len(t, f.Address, 42)
		require.False(t, seen[f.Address], "duplicate factory %s", f.Address)
		seen[f.Address] = true
	}
}

func TestPoolsSQLOnlyScansGivenFactories(t *testing.T) {
	q := poolsSQL([]string{Factories[0].Address})
	require.Contains(t, q, "address IN (unhex('"+strings.TrimPrefix(Factories[0].Address, "0x")+"'))")
	require.Equal(t, 2, strings.Count(q, "?"))
	require.Equal(t, 2, strings.Count(rollupSQL(), "?"))
}

// TestRollup_ClickHouseLocal runs the real SQL against synthetic logs in
// clickhouse-local. Set DEXVOLUME_CH_LOCAL=1 (needs the clickhouse binary).
func TestRollup_ClickHouseLocal(t *testing.T) {
	if os.Getenv("DEXVOLUME_CH_LOCAL") == "" {
		t.Skip("DEXVOLUME_CH_LOCAL not set")
	}
	schema, err := os.ReadFile("../chwrapper/raw_tables.sql")
	require.NoError(t, err)
	schema = regexp.MustCompile(`--[^\n]*`).ReplaceAll(schema, nil)
	var ddl []string
	for _, name := range []string{"raw_logs", "stablecoins", "dex_pools", "dex_daily"} {
		m := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` + name + ` \(.*?;`).Find(schema)
		require.NotNil(t, m, name)
		ddl = append(ddl, string(m))
	}

	const (
		usdc    = "b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"
		usdt    = "9702230a8ea53601f5cd2dc00fdbc13d4df4a8c7"
		exotic  = "00000000000000000000000000000000000000e1"
		p1      = "00000000000000000000000000000000000000a1" // uniswap-v3 WAVAX/USDC
		p2      = "00000000000000000000000000000000000000a2" // lfj EXOTIC/WAVAX
		p3      = "00000000000000000000000000000000000000a3" // blackhole USDT/EXOTIC
		p4      = "00000000000000000000000000000000000000a4" // no creation event
		p5      = "00000000000000000000000000000000000000a5" // created by a non-factory
		p6      = "00000000000000000000000000000000000000a6" // pangolin-v2 WAVAX/USDT (AEB)
		p7      = "00000000000000000000000000000000000000a7" // lfj v2.0 WAVAX/USDC
		aebUSDT = "de3a24028580884448a5397872046a019649b084"
		alice   = "00000000000000000000000000000000000000f1"
		bob     = "00000000000000000000000000000000000000f2"
		carol   = "00000000000000000000000000000000000000f3"
		uniFact = "740b1c1de25031c31ff4fc9a62f554a55cdc1bad"
		lbFact  = "b43120c4745967fa9b93e79c149e66b0f2d6fe0c"
		algFact = "512eb749541b7cf294be882d636218c84a5e9e5f"
		pngFact = "efa94de7a4656d787667c749f7e1223d71e9fd88"
		lb20F   = "6e77932a92582f504ff6c4bdbcef7da6c198aeef"
	)
	word := func(hexAddr string) string { return strings.Repeat("0", 64-len(hexAddr)) + hexAddr }
	i256 := func(v string) string {
		n, _ := new(big.Int).SetString(v, 10)
		if n.Sign() < 0 {
			n.Add(n, new(big.Int).Lsh(big.NewInt(1), 256))
		}
		return fmt.Sprintf("%064x", n)
	}
	packed := func(x, y string) string { // bytes32 Y<<128 | X
		xi, _ := new(big.Int).SetString(x, 10)
		yi, _ := new(big.Int).SetString(y, 10)
		return fmt.Sprintf("%064x", new(big.Int).Or(new(big.Int).Lsh(yi, 128), xi))
	}
	n := 0
	logRowTx := func(tx, addr, ts, topic0, t1, t2, t3, data, from string) string {
		nt := func(s string) string {
			if s == "" {
				return "NULL"
			}
			return "unhex('" + word(s) + "')"
		}
		n++
		return fmt.Sprintf("(43114, unhex('%s'), 1, unhex('%s'), '%s', unhex('%s'), 0, %d, unhex('%s'), NULL, unhex('%s'), %s, %s, %s, unhex('%s'), false)",
			addr, strings.Repeat("0", 64), ts, word(tx), n, from, topic0, nt(t1), nt(t2), nt(t3), data)
	}
	logRow := func(addr, ts, topic0, t1, t2, t3, data, from string) string {
		return logRowTx(fmt.Sprintf("%x", n+1000), addr, ts, topic0, t1, t2, t3, data, from)
	}
	created := "2026-09-01 00:00:00.000"
	day := "2026-10-01 "
	v3swap := func(a0, a1 string) string { return i256(a0) + i256(a1) + strings.Repeat("0", 64*3) }
	lbswap := func(in, out string) string { return strings.Repeat("0", 64) + in + out + strings.Repeat("0", 64*3) }
	u256 := func(v string) string { return i256(v) }
	v2swap := func(in0, in1, out0, out1 string) string { return u256(in0) + u256(in1) + u256(out0) + u256(out1) }
	lb20swap := func(forY bool, in, out string) string {
		y := "0"
		if forY {
			y = "1"
		}
		return u256(y) + u256(in) + u256(out) + strings.Repeat("0", 64*2)
	}
	rows := []string{
		logRow(uniFact, created, topicPoolCreated, WAVAX, usdc, "1f4", word("a")+word(p1), alice),
		logRow(lbFact, created, topicLBPairCreated, exotic, WAVAX, "14", word(p2)+word("1"), alice),
		logRow(algFact, created, topicAlgebraCustom, "dead", usdt, exotic, word(p3), alice),
		logRow("00000000000000000000000000000000000000bb", created, topicPoolCreated, WAVAX, usdc, "1f4", word("a")+word(p5), alice),
		logRow(pngFact, created, topicPairCreated, WAVAX, aebUSDT, "", word(p6)+word("1"), alice),
		logRow(lb20F, created, topicLBPairCreated, WAVAX, usdc, "14", word(p7)+word("0"), alice),
		// WAVAX in, USDC out at $9, and the reverse: $90 + $900
		logRow(p1, day+"10:00:00.000", topicSwapV3, alice, alice, "", v3swap("10000000000000000000", "-90000000"), alice),
		logRow(p1, day+"10:30:00.000", topicSwapV3, bob, bob, "", v3swap("-100000000000000000000", "900000000"), bob),
		// 5 EXOTIC in for 50 WAVAX out: priced at the hour's AVAX price, $450
		logRow(p2, day+"10:45:00.000", topicSwapLB, alice, alice, "", lbswap(packed("5000000000000000000", "0"), packed("0", "50000000000000000000")), alice),
		// V2: 10 WAVAX in for 95 USDT (AEB) out, priced by the legacy stablecoin list: $95
		logRow(p6, day+"10:50:00.000", topicSwapV2, alice, alice, "", v2swap("10000000000000000000", "0", "0", "95000000"), alice),
		// V2 flash swap repaid in the same token counts once: $100
		logRow(p6, day+"11:00:00.000", topicSwapV2, alice, alice, "", v2swap("0", "100000000", "0", "100000000"), alice),
		// LB v2.0 swap crossing two bins in one tx: one swap, 3 WAVAX for $27
		logRowTx("b20", p7, day+"10:55:00.000", topicSwapLB20, alice, alice, "8000", lb20swap(true, "1000000000000000000", "9000000"), alice),
		logRowTx("b20", p7, day+"10:55:00.000", topicSwapLB20, alice, alice, "8001", lb20swap(true, "2000000000000000000", "18000000"), alice),
		// A V2 Swap event from an LB v2.0 pool does not match its kind: ignored
		logRow(p7, day+"11:00:00.000", topicSwapV2, alice, alice, "", v2swap("0", "100000000", "0", "100000000"), alice),
		// $2M of USDT: priced and flagged
		logRow(p3, day+"12:00:00.000", topicSwapV3, carol, carol, "", v3swap("2000000000000", "-1"), carol),
		// Not factory pools: ignored
		logRow(p4, day+"12:00:00.000", topicSwapV3, carol, carol, "", v3swap("5000000000000", "-1"), carol),
		logRow(p5, day+"12:00:00.000", topicSwapV3, carol, carol, "", v3swap("-1", "7000000000000"), carol),
		// Next day: outside the rolled-up range
		logRow(p1, "2026-10-02 01:00:00.000", topicSwapV3, alice, alice, "", v3swap("1", "-5000000000"), alice),
		// A day with no WAVAX/stablecoin swap: no AVAX price, so the WAVAX leg stays unpriced (not NaN)
		logRow(p2, "2026-10-03 09:00:00.000", topicSwapLB, bob, bob, "", lbswap(packed("5000000000000000000", "0"), packed("0", "50000000000000000000")), bob),
	}

	bind := func(q string, args ...string) string {
		for _, a := range args {
			q = strings.Replace(q, "?", "'"+a+"'", 1)
		}
		return q
	}
	script := strings.Join(ddl, "\n") + "\n" +
		"INSERT INTO stablecoins (chain_id, token, symbol, decimals) VALUES (43114, unhex('" + usdc + "'), 'USDC', 6), (43114, unhex('" + usdt + "'), 'USDt', 6);\n" +
		"INSERT INTO raw_logs VALUES " + strings.Join(rows, ",\n") + ";\n" +
		bind(poolsSQL(func() []string {
			var a []string
			for _, f := range Factories {
				a = append(a, f.Address)
			}
			return a
		}()), "2022-01-01 00:00:00", "2027-01-01 00:00:00") + ";\n" +
		bind(rollupSQL(), "2026-10-01 00:00:00", "2026-10-02 00:00:00") + ";\n" +
		bind(rollupSQL(), "2026-10-03 00:00:00", "2026-10-04 00:00:00") + ";\n" +
		"SELECT dex, lower(hex(pool)), swaps, priced_swaps, round(volume_usd, 2), flagged_swaps, uniqExactMerge(addresses) FROM dex_daily FINAL WHERE day = '2026-10-01' GROUP BY dex, pool, swaps, priced_swaps, volume_usd, flagged_swaps ORDER BY dex, pool FORMAT TSV;\n" +
		"SELECT uniqExactMerge(addresses), count() FROM dex_daily FINAL WHERE day = '2026-10-01' FORMAT TSV;\n" +
		"SELECT dex, swaps, priced_swaps, volume_usd, isFinite(volume_usd) FROM dex_daily FINAL WHERE day = '2026-10-03' FORMAT TSV;\n" +
		"SELECT count() FROM dex_pools FINAL FORMAT TSV;\n"

	if dump := os.Getenv("DEXVOLUME_DUMP"); dump != "" {
		_ = os.WriteFile(dump, []byte(script), 0o644)
	}
	f, err := os.CreateTemp(t.TempDir(), "q*.sql")
	require.NoError(t, err)
	_, _ = f.WriteString(script)
	f.Close()
	start := time.Now()
	out, err := exec.Command("clickhouse", "local", "--multiquery", "--queries-file", f.Name()).CombinedOutput()
	require.NoError(t, err, string(out))
	t.Logf("clickhouse-local %s:\n%s", time.Since(start), out)

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	require.Equal(t, []string{
		"blackhole-clmm\t" + p3 + "\t1\t1\t2000000\t1\t1",
		"lfj\t" + p2 + "\t1\t1\t450\t0\t1",
		"lfj\t" + p7 + "\t1\t1\t27\t0\t1",
		"pangolin-v2\t" + p6 + "\t2\t2\t195\t0\t1",
		"uniswap-v3\t" + p1 + "\t2\t2\t990\t0\t2",
		"3\t5",
		"lfj\t1\t0\t0\t1",
		"5",
	}, lines)
}
