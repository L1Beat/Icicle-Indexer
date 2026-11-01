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
		alice   = "00000000000000000000000000000000000000f1"
		bob     = "00000000000000000000000000000000000000f2"
		carol   = "00000000000000000000000000000000000000f3"
		uniFact = "740b1c1de25031c31ff4fc9a62f554a55cdc1bad"
		lbFact  = "b43120c4745967fa9b93e79c149e66b0f2d6fe0c"
		algFact = "512eb749541b7cf294be882d636218c84a5e9e5f"
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
	logRow := func(addr, ts, topic0, t1, t2, t3, data, from string) string {
		nt := func(s string) string {
			if s == "" {
				return "NULL"
			}
			return "unhex('" + word(s) + "')"
		}
		return fmt.Sprintf("(43114, unhex('%s'), 1, unhex('%s'), '%s', unhex('%s'), 0, 0, unhex('%s'), NULL, unhex('%s'), %s, %s, %s, unhex('%s'), false)",
			addr, strings.Repeat("0", 64), ts, strings.Repeat("0", 64), from, topic0, nt(t1), nt(t2), nt(t3), data)
	}
	created := "2026-09-01 00:00:00.000"
	day := "2026-10-01 "
	v3swap := func(a0, a1 string) string { return i256(a0) + i256(a1) + strings.Repeat("0", 64*3) }
	lbswap := func(in, out string) string { return strings.Repeat("0", 64) + in + out + strings.Repeat("0", 64*3) }
	rows := []string{
		logRow(uniFact, created, topicPoolCreated, WAVAX, usdc, "1f4", word("a")+word(p1), alice),
		logRow(lbFact, created, topicLBPairCreated, exotic, WAVAX, "14", word(p2)+word("1"), alice),
		logRow(algFact, created, topicAlgebraCustom, "dead", usdt, exotic, word(p3), alice),
		logRow("00000000000000000000000000000000000000bb", created, topicPoolCreated, WAVAX, usdc, "1f4", word("a")+word(p5), alice),
		// WAVAX in, USDC out at $9, and the reverse: $90 + $900
		logRow(p1, day+"10:00:00.000", topicSwapV3, alice, alice, "", v3swap("10000000000000000000", "-90000000"), alice),
		logRow(p1, day+"10:30:00.000", topicSwapV3, bob, bob, "", v3swap("-100000000000000000000", "900000000"), bob),
		// 5 EXOTIC in for 50 WAVAX out: priced at the hour's AVAX price, $450
		logRow(p2, day+"10:45:00.000", topicSwapLB, alice, alice, "", lbswap(packed("5000000000000000000", "0"), packed("0", "50000000000000000000")), alice),
		// $2M of USDT: priced and flagged
		logRow(p3, day+"12:00:00.000", topicSwapV3, carol, carol, "", v3swap("2000000000000", "-1"), carol),
		// Not factory pools: ignored
		logRow(p4, day+"12:00:00.000", topicSwapV3, carol, carol, "", v3swap("5000000000000", "-1"), carol),
		logRow(p5, day+"12:00:00.000", topicSwapV3, carol, carol, "", v3swap("-1", "7000000000000"), carol),
		// Next day: outside the rolled-up range
		logRow(p1, "2026-10-02 01:00:00.000", topicSwapV3, alice, alice, "", v3swap("1", "-5000000000"), alice),
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
		"SELECT dex, lower(hex(pool)), swaps, priced_swaps, round(volume_usd, 2), flagged_swaps, uniqExactMerge(addresses) FROM dex_daily FINAL GROUP BY dex, pool, swaps, priced_swaps, volume_usd, flagged_swaps ORDER BY dex FORMAT TSV;\n" +
		"SELECT uniqExactMerge(addresses), count() FROM dex_daily FINAL FORMAT TSV;\n" +
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
		"uniswap-v3\t" + p1 + "\t2\t2\t990\t0\t2",
		"3\t3",
		"3",
	}, lines)
}
