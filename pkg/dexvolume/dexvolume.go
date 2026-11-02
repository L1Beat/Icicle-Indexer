// Package dexvolume rolls up C-Chain DEX swap volume per UTC day from raw_logs.
// Only pools whose creation event was emitted by a known factory are counted,
// so contracts that merely emit a matching Swap event add nothing. Swaps are
// priced conservatively: a USD stablecoin side at $1, else a WAVAX side at the
// hour's AVAX price, else left unpriced.
package dexvolume

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"icicle/pkg/chwrapper"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ChainID is the C-Chain. Only it is rolled up.
const ChainID = 43114

// Kind is the Swap event family a pool emits.
type Kind string

const (
	KindV3 Kind = "v3" // Uniswap V3 layout: amount0, amount1 int256 (also Algebra)
	KindLB Kind = "lb" // Liquidity Book: amountsIn, amountsOut packed as Y<<128 | X
)

// Dex is one DEX as served by the API.
type Dex struct {
	ID       string
	Name     string
	Protocol string // parent protocol, to group e.g. Pharaoh V3 and DLMM
	Kind     Kind
}

// Dexes in display order.
var Dexes = []Dex{
	{ID: "pharaoh-v3", Name: "Pharaoh V3", Protocol: "pharaoh", Kind: KindV3},
	{ID: "pharaoh-dlmm", Name: "Pharaoh DLMM", Protocol: "pharaoh", Kind: KindLB},
	{ID: "uniswap-v3", Name: "Uniswap V3", Protocol: "uniswap", Kind: KindV3},
	{ID: "blackhole-clmm", Name: "Blackhole CLMM", Protocol: "blackhole", Kind: KindV3},
	{ID: "lfj", Name: "LFJ", Protocol: "lfj", Kind: KindLB},
	{ID: "pangolin-v3", Name: "Pangolin V3", Protocol: "pangolin", Kind: KindV3},
}

// Factory is a pool factory and the event it emits when it creates a pool.
type Factory struct {
	Address string // lowercase
	Dex     string
	Note    string
}

// Factories, verified on-chain 2026-11-02 (contract names on Routescan, and the
// same addresses in DefiLlama's TVL registries).
var Factories = []Factory{
	{"0xae6e5c62328ade73ceefd42228528b70c8157d0d", "pharaoh-v3", "RamsesV3Factory"},
	{"0xaaa32926fce6be95ea2c51cb4fcb60836d320c42", "pharaoh-v3", "Pharaoh CL factory (legacy)"},
	{"0xeb480050b016f6c6d45203d2346b68bddda23d4d", "pharaoh-dlmm", "DLMMFactory"},
	{"0x740b1c1de25031c31ff4fc9a62f554a55cdc1bad", "uniswap-v3", "UniswapV3Factory"},
	{"0x512eb749541b7cf294be882d636218c84a5e9e5f", "blackhole-clmm", "AlgebraFactory"},
	{"0xb43120c4745967fa9b93e79c149e66b0f2d6fe0c", "lfj", "LBFactory V2.1"},
	{"0x8e42f2f4101563bf679975178e880fd87d3efd4e", "lfj", "LBFactory V2.2"},
	{"0x1128f23d0bc0a8396e9fbc3c0c68f5ea228b8256", "pangolin-v3", "PangolinV3Factory"},
}

// Event topics.
const (
	topicSwapV3        = "c42079f94a6350d7e6235f29174924f928cc2ac818eb64fed8004e115fbcca67" // Swap(address,address,int256,int256,uint160,uint128,int24)
	topicSwapLB        = "ad7d6f97abf51ce18e17a38f4d70e975be9c0708474987bb3e26ad21bd93ca70" // Swap(address,address,uint24,bytes32,bytes32,uint24,bytes32,bytes32)
	topicPoolCreated   = "783cca1c0412dd0d695e784568c96da2e9c22ff989357a2e8b1d9b2b4e6b7118" // PoolCreated(address,address,uint24,int24,address): pool in data word 1
	topicLBPairCreated = "2c8d104b27c6b7f4492017a6f5cf3803043688934ebcaa6a03540beeaf976aff" // LBPairCreated(address,address,uint256,address,uint256): pair in word 0
	topicAlgebraPool   = "91ccaa7a278130b65168c3a0c8d3bcae84cf5e43704342bd3ec0b59e59c036db" // Pool(address,address,address): pool in word 0
	topicAlgebraCustom = "8a5f030f5fc13b04a1e4ef7c47177e3d76b0e80e1d9be9843db37caa5b7b9b8f" // CustomPool(address,address,address,address): tokens in topics 2,3
)

// WAVAX prices swaps that have no stablecoin side.
const WAVAX = "b31f66aa3c1e785363f0875a1b74e27b85fd66c7"

// FlagUSD is the swap size above which a priced swap is counted in
// flagged_swaps. Flagged swaps stay in the volume.
const FlagUSD = 1_000_000

// BackfillDays is how far back the first forward run rolls up. Older days, down
// to the first tracked pool, are filled backward over the following cycles.
const BackfillDays = 365

// poolScanWindowDays bounds one creation-event scan INSERT.
const poolScanWindowDays = 90

// maxDaysPerInsert bounds one rollup INSERT.
const maxDaysPerInsert = 31

// maxHistoryBatchesPerSync bounds the backward history fill per Sync call, so
// it does not hold up the validator cycle for long.
const maxHistoryBatchesPerSync = 4

// historyWatermark names the indexer_watermarks row holding the first day the
// backward fill has covered (last_period).
const historyWatermark = "dexvolume/daily_from"

// poolHistoryStart bounds the one-time scan for factory creation events. All
// tracked factories were deployed after it.
var poolHistoryStart = time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)

// Sync registers new pools, rolls up every complete UTC day not yet in
// dex_daily, then extends history backward toward the first tracked pool a few
// batches per call. It is cheap when there is nothing to do and safe to re-run.
func Sync(ctx context.Context, conn driver.Conn) error {
	if err := syncPools(ctx, conn); err != nil {
		return fmt.Errorf("pools: %w", err)
	}
	if err := syncDays(ctx, conn); err != nil {
		return err
	}
	if err := repairNonFinite(ctx, conn); err != nil {
		return err
	}
	return backfillHistory(ctx, conn)
}

// syncPools scans factory creation events: the full history for any factory
// with no registered pools yet (first run, or a factory added later), and the
// last 3 days for the rest.
func syncPools(ctx context.Context, conn driver.Conn) error {
	rows, err := conn.Query(ctx, `
		SELECT lower(hex(factory)), count() FROM dex_pools FINAL GROUP BY factory`)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for rows.Next() {
		var f string
		var n uint64
		if err := rows.Scan(&f, &n); err != nil {
			rows.Close()
			return err
		}
		known["0x"+f] = n > 0
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var fresh, seen []string
	for _, f := range Factories {
		if known[f.Address] {
			seen = append(seen, f.Address)
		} else {
			fresh = append(fresh, f.Address)
		}
	}
	if len(fresh) > 0 {
		start := time.Now()
		if err := insertPools(ctx, conn, fresh, poolHistoryStart); err != nil {
			return err
		}
		slog.Info("DEX pool history scanned", "factories", len(fresh), "duration", time.Since(start))
	}
	if len(seen) > 0 {
		return insertPools(ctx, conn, seen, time.Now().UTC().Add(-72*time.Hour))
	}
	return nil
}

// insertPools scans creation events from since to now in windows, so the
// one-time history scan stays inside the write timeout.
func insertPools(ctx context.Context, conn driver.Conn, factories []string, since time.Time) error {
	q := poolsSQL(factories)
	now := time.Now().UTC().Add(time.Hour)
	for from := since; from.Before(now); from = from.AddDate(0, 0, poolScanWindowDays) {
		to := from.AddDate(0, 0, poolScanWindowDays)
		wctx, cancel := chwrapper.WriteContext(ctx)
		err := conn.Exec(wctx, q, from, to)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// poolsSQL builds the creation-event scan. The address filter is what makes a
// pool count: the event must come from the factory itself.
func poolsSQL(factories []string) string {
	addrs := make([]string, len(factories))
	for i, f := range factories {
		addrs[i] = fmt.Sprintf("unhex('%s')", strings.TrimPrefix(f, "0x"))
	}
	var fa, fd []string
	for _, f := range Factories {
		fa = append(fa, "'"+strings.TrimPrefix(f.Address, "0x")+"'")
		fd = append(fd, "'"+f.Dex+"'")
	}
	return fmt.Sprintf(`
		INSERT INTO dex_pools (pool, dex, factory, token0, token1, created_time)
		SELECT
			toFixedString(if(topic0 = unhex('%[2]s'), substring(data, 45, 20), substring(data, 13, 20)), 20) AS pool,
			transform(lower(hex(address)), [%[3]s], [%[4]s], '') AS dex,
			address AS factory,
			toFixedString(substring(assumeNotNull(if(topic0 = unhex('%[5]s'), topic2, topic1)), 13, 20), 20) AS token0,
			toFixedString(substring(assumeNotNull(if(topic0 = unhex('%[5]s'), topic3, topic2)), 13, 20), 20) AS token1,
			block_time AS created_time
		FROM raw_logs
		WHERE chain_id = %[6]d
		  AND address IN (%[1]s)
		  AND topic0 IN (unhex('%[2]s'), unhex('%[7]s'), unhex('%[8]s'), unhex('%[5]s'))
		  AND block_time >= ? AND block_time < ?
		  AND length(data) >= 32
	`, strings.Join(addrs, ", "), topicPoolCreated, strings.Join(fa, ", "), strings.Join(fd, ", "),
		topicAlgebraCustom, ChainID, topicLBPairCreated, topicAlgebraPool)
}

// syncDays rolls up complete days after the newest one in dex_daily, starting
// BackfillDays back on the first run. A day is complete once the C-Chain sync
// watermark has passed its end.
func syncDays(ctx context.Context, conn driver.Conn) error {
	var synced time.Time
	if err := conn.QueryRow(ctx, `
		SELECT block_time FROM raw_blocks
		WHERE chain_id = ? AND block_number = (SELECT block_number FROM sync_watermark WHERE chain_id = ?)
	`, uint32(ChainID), uint32(ChainID)).Scan(&synced); err != nil {
		return fmt.Errorf("sync watermark time: %w", err)
	}
	end := synced.UTC().Truncate(24 * time.Hour) // exclusive: first incomplete day

	var last time.Time
	if err := conn.QueryRow(ctx, `SELECT max(day) FROM dex_daily`).Scan(&last); err != nil {
		return err
	}
	from := last.UTC().AddDate(0, 0, 1)
	if last.Year() < 2000 { // empty table: max() is the zero date
		from = end.AddDate(0, 0, -BackfillDays)
	}

	for from.Before(end) {
		to := from.AddDate(0, 0, maxDaysPerInsert)
		if to.After(end) {
			to = end
		}
		start := time.Now()
		if err := rollup(ctx, conn, from, to); err != nil {
			return fmt.Errorf("rollup %s..%s: %w", from.Format("2006-01-02"), to.Format("2006-01-02"), err)
		}
		slog.Info("DEX volume rolled up", "from", from.Format("2006-01-02"), "to", to.Format("2006-01-02"), "duration", time.Since(start))
		from = to
	}
	return nil
}

func rollup(ctx context.Context, conn driver.Conn, from, to time.Time) error {
	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	return conn.Exec(wctx, rollupSQL(), from, to)
}

// rollupSQL builds the daily rollup over [?, ?).
//
// Amounts: a V3 swap's |amount0| and |amount1|; an LB swap's amountsIn plus
// amountsOut per token (only one side of each is non-zero). Price: the hour's
// median AVAX price implied by WAVAX/stablecoin swaps of at least $10 in the
// same tracked pools, falling back to the day's median. A day with no such
// swaps has no price, and its WAVAX swaps stay unpriced.
func rollupSQL() string {
	var lb []string
	for _, d := range Dexes {
		if d.Kind == KindLB {
			lb = append(lb, "'"+d.ID+"'")
		}
	}
	return fmt.Sprintf(`
		INSERT INTO dex_daily (day, dex, pool, swaps, priced_swaps, volume_usd, flagged_swaps, addresses)
		WITH
			(SELECT groupArray(token) FROM stablecoins FINAL WHERE chain_id = %[1]d AND peg = 'USD') AS st,
			(SELECT groupArray(decimals) FROM stablecoins FINAL WHERE chain_id = %[1]d AND peg = 'USD') AS sd,
			unhex('%[2]s') AS wavax
		SELECT
			toDate(t) AS day, dex, pool,
			count() AS swaps,
			countIf(usd IS NOT NULL) AS priced_swaps,
			sum(ifNull(usd, 0)) AS volume_usd,
			countIf(usd > %[5]d) AS flagged_swaps,
			uniqExactState(sender) AS addresses
		FROM (
			SELECT t, sender, pool, dex,
				multiIf(s0 IS NOT NULL, s0, s1 IS NOT NULL, s1,
					t0 = wavax AND isFinite(px), a0 / 1e18 * px, t1 = wavax AND isFinite(px), a1 / 1e18 * px, NULL) AS usd
			FROM (
				SELECT *, if(isFinite(px_hour), px_hour, px_day) AS px
				FROM (
				SELECT *,
					medianExactIf(ws / wa, wa > 0 AND ws >= 10) OVER (PARTITION BY toStartOfHour(t)) AS px_hour,
					medianExactIf(ws / wa, wa > 0 AND ws >= 10) OVER (PARTITION BY toDate(t)) AS px_day
				FROM (
					SELECT *,
						if(indexOf(st, t0) > 0, a0 / pow(10, sd[indexOf(st, t0)]), NULL) AS s0,
						if(indexOf(st, t1) > 0, a1 / pow(10, sd[indexOf(st, t1)]), NULL) AS s1,
						multiIf(t0 = wavax AND s1 IS NOT NULL, a0 / 1e18, t1 = wavax AND s0 IS NOT NULL, a1 / 1e18, 0) AS wa,
						ifNull(coalesce(s0, s1), 0) AS ws
					FROM (
						SELECT l.block_time AS t, l.tx_from AS sender, l.address AS pool, p.dex AS dex,
							p.token0 AS t0, p.token1 AS t1,
							if(l.topic0 = unhex('%[3]s'),
								abs(toFloat64(reinterpretAsInt256(reverse(substring(l.data, 1, 32))))),
								toFloat64(reinterpretAsUInt128(reverse(substring(l.data, 49, 16))))
									+ toFloat64(reinterpretAsUInt128(reverse(substring(l.data, 81, 16))))) AS a0,
							if(l.topic0 = unhex('%[3]s'),
								abs(toFloat64(reinterpretAsInt256(reverse(substring(l.data, 33, 32))))),
								toFloat64(reinterpretAsUInt128(reverse(substring(l.data, 33, 16))))
									+ toFloat64(reinterpretAsUInt128(reverse(substring(l.data, 65, 16))))) AS a1
						FROM raw_logs l
						INNER JOIN (SELECT pool, dex, token0, token1 FROM dex_pools FINAL) p ON l.address = p.pool
						WHERE l.chain_id = %[1]d
						  AND l.topic0 IN (unhex('%[3]s'), unhex('%[4]s'))
						  AND l.block_time >= ? AND l.block_time < ?
						  AND length(l.data) >= 96
						  AND (l.topic0 = unhex('%[4]s')) = (p.dex IN (%[6]s))
					)
				)
				)
			)
		)
		GROUP BY day, dex, pool
	`, ChainID, WAVAX, topicSwapV3, topicSwapLB, FlagUSD, strings.Join(lb, ", "))
}

// backfillHistory rolls up days before the oldest covered day, newest first,
// down to the day the first tracked pool was created. Progress is kept in
// indexer_watermarks, so days without swaps are not retried.
func backfillHistory(ctx context.Context, conn driver.Conn) error {
	var first time.Time
	if err := conn.QueryRow(ctx, `SELECT toDate(min(created_time)) FROM dex_pools FINAL`).Scan(&first); err != nil {
		return err
	}
	if first.Year() < 2000 {
		return nil
	}

	var covered time.Time
	rows, err := conn.Query(ctx, `
		SELECT last_period FROM indexer_watermarks FINAL
		WHERE chain_id = ? AND indexer_name = ?`, uint32(ChainID), historyWatermark)
	if err != nil {
		return err
	}
	for rows.Next() {
		if err := rows.Scan(&covered); err != nil {
			rows.Close()
			return err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if covered.IsZero() {
		if err := conn.QueryRow(ctx, `SELECT min(day) FROM dex_daily`).Scan(&covered); err != nil {
			return err
		}
		if covered.Year() < 2000 {
			return nil // nothing rolled up forward yet
		}
	}
	covered = covered.UTC().Truncate(24 * time.Hour)
	first = first.UTC().Truncate(24 * time.Hour)

	for i := 0; i < maxHistoryBatchesPerSync && covered.After(first); i++ {
		from := covered.AddDate(0, 0, -maxDaysPerInsert)
		if from.Before(first) {
			from = first
		}
		start := time.Now()
		if err := rollup(ctx, conn, from, covered); err != nil {
			return fmt.Errorf("history rollup %s..%s: %w", from.Format("2006-01-02"), covered.Format("2006-01-02"), err)
		}
		wctx, cancel := chwrapper.WriteContext(ctx)
		err := conn.Exec(wctx, `
			INSERT INTO indexer_watermarks (chain_id, indexer_name, granularity, last_period, last_block_num)
			VALUES (?, ?, 'day', ?, 0)`, uint32(ChainID), historyWatermark, from)
		cancel()
		if err != nil {
			return err
		}
		slog.Info("DEX history rolled up", "from", from.Format("2006-01-02"), "to", covered.Format("2006-01-02"), "duration", time.Since(start))
		covered = from
	}
	return nil
}

// repairNonFinite re-rolls days whose stored volume is NaN or infinite. Rollups
// before the price guard could store NaN for WAVAX swaps on days without a
// WAVAX/stablecoin price, and JSON cannot carry NaN.
func repairNonFinite(ctx context.Context, conn driver.Conn) error {
	rows, err := conn.Query(ctx, `SELECT DISTINCT day FROM dex_daily FINAL WHERE NOT isFinite(volume_usd) ORDER BY day`)
	if err != nil {
		return err
	}
	var days []time.Time
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			rows.Close()
			return err
		}
		days = append(days, d.UTC())
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, d := range days {
		if err := rollup(ctx, conn, d, d.AddDate(0, 0, 1)); err != nil {
			return fmt.Errorf("repair %s: %w", d.Format("2006-01-02"), err)
		}
		slog.Info("DEX day re-rolled (non-finite volume)", "day", d.Format("2006-01-02"))
	}
	return nil
}
