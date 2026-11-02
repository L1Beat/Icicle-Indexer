// Package bridges records C-Chain bridge transfers, one row per leg into or
// out of the C-Chain, from raw_logs. Each window of raw_logs is scanned once:
// the matching logs are copied to bridge_logs, the contracts behind generic
// events are checked on-chain, and the legs are decoded from bridge_logs.
//
// Tracked: Circle CCTP V1 and V2, LayerZero OFTs V1 and V2 (Stargate V2 pools
// reported apart), the Avalanche Bridge (.e tokens to Ethereum, BTC.b to
// Bitcoin), its 2021 predecessor the Avalanche-Ethereum Bridge (AEB) and
// Chainlink CCIP token pools. Each transfer is counted once: a
// CCIP or other leg moving native USDC in a transaction that also has a CCTP
// leg in the same direction is folded into the CCTP leg (via). Transactions
// that bridge in and out again are kept but marked passthrough.
package bridges

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"icicle/pkg/chwrapper"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ChainID is the C-Chain. Only it is tracked.
const ChainID = 43114

// Bridge is one bridge as served by the API.
type Bridge struct {
	ID   string
	Name string
}

// Bridges in display order.
var Bridges = []Bridge{
	{ID: "cctp", Name: "Circle CCTP"},
	{ID: "layerzero", Name: "LayerZero OFT"},
	{ID: "stargate", Name: "Stargate"},
	{ID: "avalanche-bridge", Name: "Avalanche Bridge"},
	{ID: "ccip", Name: "Chainlink CCIP"},
	{ID: "aeb", Name: "Avalanche-Ethereum Bridge"},
}

// Contracts, verified on-chain 2026-11-03 (contract names on Routescan,
// localDomain() = 1 on both CCTP MessageTransmitters).
const (
	cctpTokenMessengerV1     = "6b25532e1060ce10cc3b0a99e5683b91bfde6982"
	cctpMessageTransmitterV1 = "8186359af5f57fbb40c6b14a588d2a59c0c29880"
	cctpTokenMessengerV2     = "28b5a0e9c621a5badaa536219b3a228c8168cf5d"
	cctpMessageTransmitterV2 = "81d40f21f12a8f0e3252bccb954d722d4c464b64"

	lzEndpointV1 = "3c2269811836af69497e5f486a85d7316753cf62" // Endpoint
	lzEndpointV2 = "1a44076050125825900e736c501f859c50fe728c" // EndpointV2

	ccipTokenAdminRegistry = "c8df5d618c6a59cc6a311e96a39450381001464f"

	// BTCb is BTC.b. Its Bitcoin deposits mint with the Bitcoin tx id, and
	// since 2025 withdrawals burn through Lombard's AssetRouter, which sends a
	// redeemForBtc request through Lombard's Mailbox.
	BTCb             = "152b9d0fdc40c096757f570a51e494bd4b943e50"
	lombardMailbox   = "964677f337d6528d659b1892d0045b8b27183fc0"
	selectorRedeem   = "4e3e5047" // Assets.REDEEM_FROM_NATIVE_TOKEN_SELECTOR
	redeemSelectorAt = 293        // 1-based byte of the selector in MessageSent data

	// aebBridge is the ChainSafe ChainBridge behind the Avalanche-Ethereum
	// Bridge (Jan-Aug 2021). Its handler mints a token on an executed
	// proposal and burns it on a deposit, in the same tx as the Bridge event.
	aebBridge = "6460777cda22ad67bbb97536ffc446d65761197e"

	// USDC is native USDC, the only token CCTP moves on the C-Chain.
	USDC = "b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"
)

// lzV1Libraries are the LayerZero V1 message libraries. A V1 OFT event counts
// only in a transaction where one of them logged the message.
var lzV1Libraries = []string{
	"4d73adb72bc3dd368966edd0f0b2148401a178e2", // UltraLightNodeV2
	"31cae3b7fb82d847621859fb1585353c5720660d", // SendUln301
	"f85ed5489e6add01fec9e8d53cf8facfc70590bd", // ReceiveUln301
}

// stargatePools are Stargate V2 pools. They emit OFT events and are reported
// as stargate rather than layerzero.
var stargatePools = map[string]bool{
	"5634c4a5fed09819e3c46d86a965dd9447d86e47": true, // StargatePoolUSDC
	"12dc9256acc9895b076f6638d628382881e62cee": true, // StargatePoolMigratable (USDt)
}

// priceFeeds are the Chainlink aggregators behind each USD feed's proxy, every
// phase (proxy phaseAggregators), 8 decimals.
var priceFeeds = []struct{ Aggregator, Feed string }{
	{"efaa69f461e0aaf0be1798b01371daf14ac55ea8", "ETH"},  // ETH / USD 0x976B3D03... phase 1
	{"9ff520924dc295074f43a8a01ee30a4d39a4223b", "ETH"},  // phase 2
	{"154bab1fc1d87ff641eed0e9bc0f8a50d880d2b6", "BTC"},  // BTC / USD 0x2779D32d... phase 1
	{"a9afa74ddac812b86eeaa60a035c6592470f4a48", "BTC"},  // phase 2
	{"9450a29ef091b625e976ce66f2a5818e20791999", "AVAX"}, // AVAX / USD 0x0A77230d... phase 1
	{"cfbfe648ba88bf7d9776d970417a0b26c315feb4", "AVAX"}, // phase 2
	{"a2e5d3254f7d6e8c051afb7f2aeea0dabf21f750", "LINK"}, // LINK / USD 0x49ccd9ca... phase 1
	{"9fc8683eff42913f0f1f32eec2f21340017fcddb", "LINK"}, // phase 2
}

// pricedTokens are non-stablecoin tokens priced at a feed: bridged and wrapped
// versions of the feed's asset, redeemable one to one. USD stablecoins come
// from the stablecoin registry.
var pricedTokens = []struct {
	Token    string
	Feed     string
	Decimals uint8
}{
	{"49d5c2bdffac6ce2bfdb6640f4f80f226bc10bab", "ETH", 18}, // WETH.e
	{BTCb, "BTC", 8}, // BTC.b
	{"50b7545627a5162f82a992c33b87adc75187b218", "BTC", 8},   // WBTC.e
	{"0555e30da8f98308edb960aa94c0db47230d2b9c", "BTC", 8},   // WBTC (LayerZero OFT)
	{"ecac9c5f704e954931349da37f60e39f515c11c1", "BTC", 8},   // LBTC
	{"b31f66aa3c1e785363f0875a1b74e27b85fd66c7", "AVAX", 18}, // WAVAX
	{"5947bb275c521040051d82396192181b413227a3", "LINK", 18}, // LINK.e
	{"f20d962a6c8f70c731bd838a3a388d7d48fa6e15", "ETH", 18},  // ETH (AEB)
	{"408d4cd0adb7cebd1f1a1c33a0ba2098e1295bab", "BTC", 8},   // WBTC (AEB)
	{"b3fe5374f67d7a22886a0ee082b2e2f9d2651651", "LINK", 18}, // LINK (AEB)
}

// legacyUSD are the Avalanche-Ethereum Bridge stablecoins, priced at $1 on top
// of the stablecoin registry, which only lists live tokens.
var legacyUSD = []struct {
	Token    string
	Decimals uint8
}{
	{"de3a24028580884448a5397872046a019649b084", 6},  // USDT (AEB)
	{"ba7deebbfc5fa1100fb055a87773e1e99cd3507a", 18}, // DAI (AEB)
	{"aeb044650278731ef3dc244692ab9f64c78ffaea", 18}, // BUSD (AEB)
}

// Event topics.
const (
	topicCCTPDepositV1        = "2fa9ca894982930190727e75500a97d8dc500233a5065e0f3126c48fbe0343c0" // DepositForBurn(uint64,address,uint256,address,bytes32,uint32,bytes32,bytes32)
	topicCCTPDepositV2        = "0c8c1cbdc5190613ebd485511d4e2812cfa45eecb79d845893331fedad5130a5" // DepositForBurn(address,uint256,address,bytes32,uint32,bytes32,bytes32,uint256,uint32,bytes)
	topicCCTPMintV1           = "1b2a7ff080b8cb6ff436ce0372e399692bbfb6d4ae5766fd8d58a7b8cc6142e6" // MintAndWithdraw(address,uint256,address)
	topicCCTPMintV2           = "50c55e915134d457debfa58eb6f4342956f8b0616d51a89a3659360178e1ab63" // MintAndWithdraw(address,uint256,address,uint256)
	topicCCTPReceivedV1       = "58200b4c34ae05ee816d710053fff3fb75af4395915d3d2a771b24aa10e3cc5d" // MessageReceived(address,uint32,uint64,bytes32,bytes)
	topicCCTPReceivedV2       = "ff48c13eda96b1cceacc6b9edeedc9e9db9d6226afbc30146b720c19d3addb1c" // MessageReceived(address,uint32,bytes32,bytes32,uint32,bytes)
	topicOFTSent              = "85496b760a4b7f8d66384b9df21b381f5d1b1e79f229a47aaf4c232edc2fe59a" // OFTSent(bytes32,uint32,address,uint256,uint256)
	topicOFTReceived          = "efed6d3500546b29533b128a29e3a94d70788727f0507505ac12eaf2e578fd9c" // OFTReceived(bytes32,uint32,address,uint256)
	topicLZ1Send              = "d81fc9b8523134ed613870ed029d6170cbb73aa6a6bc311b9a642689fb9df59a" // SendToChain(uint16,address,bytes32,uint256)
	topicLZ1SendBytes         = "39a4c66499bcf4b56d79f0dde8ed7a9d4925a0df55825206b2b8531e202be0d0" // SendToChain(uint16,address,bytes,uint256), bytes indexed
	topicLZ1Receive           = "bf551ec93859b170f9b2141bd9298bf3f64322c6f7beb2543a0cb669834118bf" // ReceiveFromChain(uint16,address,uint256)
	topicLZ1ReceiveBytes      = "776434b505c7beb3db155c58df6c88985bf7c31730767e43ec773005059fed7a" // ReceiveFromChain(uint16,bytes,address,uint256), bytes indexed
	topicPacketSent           = "1ab700d4ced0c005b164c0f789fd09fcbb0156d4c2041b8a3bfbcd961cd1567f" // PacketSent(bytes,bytes,address)
	topicPacketDelivered      = "3cd5e48f9730b129dc7550f0fcea9c767b7be37837cd10e55eb35f734f4bca04" // PacketDelivered((uint32,bytes32,uint64),address)
	topicCCIPLockedOrBurned   = "f33bc26b4413b0e7f19f1ea739fdf99098c0061f1f87d954b11f5293fad9ae10" // LockedOrBurned(uint64,address,address,uint256)
	topicCCIPReleasedOrMinted = "fc5e3a5bddc11d92c2dc20fae6f7d5eb989f056be35239f7de7e86150609abc0" // ReleasedOrMinted(uint64,address,address,address,uint256)
	topicABMint               = "918d77674bb88eaf75afb307c9723ea6037706de68d6fc07dd0c6cba423a5250" // Mint(address,uint256,address,uint256,bytes32)
	topicBTCbMint             = "c5532043e6f3d77e1c320ff59bd4157ca9075ef59f6b55ceab6c3f7d2f78c9ca" // Mint(address,uint256,address,uint256,bytes32,uint256)
	topicABUnwrap             = "37a06799a3500428a773d00284aa706101f5ad94dae9ec37e1c3773aa54c3304" // Unwrap(uint256,uint256)
	topicMailboxSent          = "8784acf2b096380844b252c1cc85752ea2dcccf0fd55ed7a440e0ccd3b56b908" // MessageSent(bytes32,address,bytes32,bytes)
	topicAnswerUpdated        = "0559884fd3a460db3073b7fc896cc77986f16e378210ded43186175bf646fc5f" // AnswerUpdated(int256,uint256,uint256)
	topicTransfer             = "ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef" // Transfer(address,address,uint256)
	topicAEBDeposit           = "dbb69440df8433824a026ef190652f29929eb64b4d1d5d2a69be8afe3e6eaed8" // Deposit(uint8,bytes32,uint64)
	topicAEBProposal          = "803c5a12f6bde629cea32e63d4b92d1b560816a6fb72e939d3c89e1cab650417" // ProposalEvent(uint8,uint64,uint8,bytes32,bytes32)
	topicSwapV2               = "d78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822" // Swap(address,uint256,uint256,uint256,uint256,address)
)

// aebExecuted is ChainBridge's ProposalStatus.Executed.
const aebExecuted = 3

// aebChains maps ChainBridge chain ids to chain keys. The AEB only had Ethereum.
var aebChains = map[uint32]string{1: "ethereum"}

// FlagUSD is the transfer size above which a priced transfer is counted in
// flagged_transfers. Flagged transfers stay in the totals.
const FlagUSD = 10_000_000

// Start is where the first run begins, ahead of every tracked bridge (the
// AEB's first transfer is on 2021-01-28).
var Start = time.Date(2021, 1, 25, 0, 0, 0, 0, time.UTC)

// chainlinkStart is the first Chainlink answer on the C-Chain (ETH, BTC, LINK
// and AVAX feeds, 2021-07-14). Earlier hours are priced from DEX swaps.
var chainlinkStart = time.Date(2021, 7, 15, 0, 0, 0, 0, time.UTC)

// rescan is a past period scanned again from raw_logs once, after bridges or
// events were added for it. It keeps its own watermark, so the main fill is
// not rewound.
type rescan struct {
	Name     string
	From, To time.Time
}

var rescans = []rescan{
	{Name: "aeb", From: Start, To: time.Date(2021, 9, 1, 0, 0, 0, 0, time.UTC)},
}

// windowDays bounds one raw_logs scan.
const windowDays = 7

// budget bounds the work per Sync call, so the history fill does not hold up
// the C-Chain snapshot cycle for long.
const budget = 2 * time.Minute

// watermark names the indexer_watermarks row holding the time up to which
// raw_logs have been scanned and decoded (last_period, exclusive).
const watermark = "bridges/through"

// staleDays is how old the last Chainlink answer may be before a transfer is
// left unpriced.
const staleDays = 7

// Sync scans raw_logs from the watermark up to the C-Chain sync watermark, a
// week at a time, within budget: copy the bridge logs, check new contracts,
// roll up prices, decode the legs. caller reads the C-Chain (eth_call).
func Sync(ctx context.Context, conn driver.Conn, caller Caller) error {
	var synced time.Time
	if err := conn.QueryRow(ctx, `
		SELECT block_time FROM raw_blocks
		WHERE chain_id = ? AND block_number = (SELECT block_number FROM sync_watermark WHERE chain_id = ?)
	`, uint32(ChainID), uint32(ChainID)).Scan(&synced); err != nil {
		return fmt.Errorf("sync watermark time: %w", err)
	}
	synced = synced.UTC()

	from, err := Through(ctx, conn)
	if err != nil {
		return err
	}
	if from.IsZero() {
		from = Start
	}

	deadline := time.Now().Add(budget)
	// A rescan only covers what the main fill has already passed. The rest
	// the main fill picks up with the current events.
	for _, r := range rescans {
		done, err := readWatermark(ctx, conn, watermark+"/rescan/"+r.Name)
		if err != nil {
			return err
		}
		if done.IsZero() {
			done = r.From
		}
		end := r.To
		if from.Before(end) {
			end = from
		}
		if err := syncRange(ctx, conn, caller, done, end, deadline, watermark+"/rescan/"+r.Name, "Bridge rescan "+r.Name); err != nil {
			return err
		}
	}
	return syncRange(ctx, conn, caller, from, synced, deadline, watermark, "Bridge transfers synced")
}

// syncRange runs windows from from to to until done or past deadline, moving
// the named watermark after each.
func syncRange(ctx context.Context, conn driver.Conn, caller Caller, from, to, deadline time.Time, mark, msg string) error {
	for from.Before(to) && time.Now().Before(deadline) {
		end := from.AddDate(0, 0, windowDays)
		if end.After(to) {
			end = to
		}
		start := time.Now()
		if err := syncWindow(ctx, conn, caller, from, end); err != nil {
			return fmt.Errorf("window %s..%s: %w", from.Format(time.RFC3339), end.Format(time.RFC3339), err)
		}
		wctx, cancel := chwrapper.WriteContext(ctx)
		err := conn.Exec(wctx, `
			INSERT INTO indexer_watermarks (chain_id, indexer_name, granularity, last_period, last_block_num)
			VALUES (?, ?, 'time', ?, 0)`, uint32(ChainID), mark, end)
		cancel()
		if err != nil {
			return err
		}
		if end.Sub(from) > time.Hour {
			slog.Info(msg, "from", from.Format("2006-01-02"), "to", end.Format("2006-01-02"), "duration", time.Since(start))
		}
		from = end
	}
	return nil
}

// Through is the time up to which bridge transfers are complete, zero before
// the first window.
func Through(ctx context.Context, conn driver.Conn) (time.Time, error) {
	return readWatermark(ctx, conn, watermark)
}

func readWatermark(ctx context.Context, conn driver.Conn, name string) (time.Time, error) {
	var t time.Time
	rows, err := conn.Query(ctx, `
		SELECT last_period FROM indexer_watermarks FINAL
		WHERE chain_id = ? AND indexer_name = ?`, uint32(ChainID), name)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(&t); err != nil {
			return t, err
		}
	}
	return t.UTC(), rows.Err()
}

func syncWindow(ctx context.Context, conn driver.Conn, caller Caller, from, to time.Time) error {
	if err := run(ctx, conn, captureSQL(from, to)); err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	if err := discover(ctx, conn, caller, from, to); err != nil {
		return fmt.Errorf("contracts: %w", err)
	}
	if from.Before(chainlinkStart) {
		if err := run(ctx, conn, dexPricesSQL(from, to)); err != nil {
			return fmt.Errorf("dex prices: %w", err)
		}
	}
	if err := run(ctx, conn, pricesSQL(from, to)); err != nil {
		return fmt.Errorf("prices: %w", err)
	}
	if err := run(ctx, conn, deriveSQL(from, to)); err != nil {
		return fmt.Errorf("derive: %w", err)
	}
	return nil
}

func run(ctx context.Context, conn driver.Conn, q string) error {
	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	return conn.Exec(wctx, q)
}

// Helpers that build SQL fragments.

func timeLit(t time.Time) string {
	return fmt.Sprintf("toDateTime64('%s', 3, 'UTC')", t.UTC().Format("2006-01-02 15:04:05.000"))
}

func unhexList(addrs ...string) string {
	out := make([]string, len(addrs))
	for i, a := range addrs {
		out[i] = fmt.Sprintf("unhex('%s')", strings.TrimPrefix(a, "0x"))
	}
	return strings.Join(out, ", ")
}

func quoted(ss []string) string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = "'" + s + "'"
	}
	return strings.Join(out, ", ")
}

func aggregators() []string {
	out := make([]string, len(priceFeeds))
	for i, f := range priceFeeds {
		out[i] = f.Aggregator
	}
	return out
}

var zero32 = strings.Repeat("00", 32)

// captureSQL copies the window's bridge logs out of raw_logs in one scan.
// Event topics anyone can emit are copied from every address and checked when
// decoded; the rest only from the contracts that matter.
func captureSQL(from, to time.Time) string {
	return fmt.Sprintf(`
		INSERT INTO bridge_logs (block_time, block_number, transaction_hash, log_index, address, tx_from,
			topic0, topic1, topic2, topic3, data)
		SELECT block_time, block_number, transaction_hash, log_index, address, tx_from,
			topic0, topic1, topic2, topic3, data
		FROM raw_logs
		WHERE chain_id = %d
		  AND block_time >= %s AND block_time < %s
		  AND (topic0 IN (%s)
			OR (address = unhex('%s') AND topic0 IN (%s))
			OR address IN (%s)
			OR (address = unhex('%s') AND topic0 = unhex('%s'))
			OR (address IN (%s) AND topic0 = unhex('%s'))
			OR (address = unhex('%s') AND topic0 = unhex('%s') AND topic2 = unhex('%s'))
			OR (address = unhex('%s') AND topic0 IN (%s))
			OR (topic0 = unhex('%s') AND tx_to = unhex('%s') AND (topic1 = unhex('%s') OR topic2 = unhex('%s'))))
	`, ChainID, timeLit(from), timeLit(to),
		unhexList(topicCCTPDepositV1, topicCCTPDepositV2, topicCCTPMintV1, topicCCTPMintV2,
			topicCCTPReceivedV1, topicCCTPReceivedV2, topicOFTSent, topicOFTReceived,
			topicLZ1Send, topicLZ1SendBytes, topicLZ1Receive, topicLZ1ReceiveBytes,
			topicCCIPLockedOrBurned, topicCCIPReleasedOrMinted, topicABMint, topicBTCbMint, topicABUnwrap),
		lzEndpointV2, unhexList(topicPacketSent, topicPacketDelivered),
		unhexList(lzV1Libraries...),
		lombardMailbox, topicMailboxSent,
		unhexList(aggregators()...), topicAnswerUpdated,
		BTCb, topicTransfer, zero32,
		aebBridge, unhexList(topicAEBDeposit, topicAEBProposal),
		topicTransfer, aebBridge, zero32, zero32)
}

// pricesSQL rolls the window's Chainlink answers up to the last one per hour.
// An hour split across two windows is written twice, and the later answer wins.
func pricesSQL(from, to time.Time) string {
	var aggs, feeds []string
	for _, f := range priceFeeds {
		aggs = append(aggs, f.Aggregator)
		feeds = append(feeds, f.Feed)
	}
	return fmt.Sprintf(`
		INSERT INTO price_hourly (feed, hour, price, answered_at)
		SELECT feed, toStartOfHour(block_time) AS hour, argMax(price, (block_time, log_index)), max(block_time)
		FROM (
			SELECT transform(lower(hex(address)), [%s], [%s], '') AS feed,
				toFloat64(reinterpretAsInt256(reverse(assumeNotNull(topic1)))) / 1e8 AS price,
				block_time, log_index
			FROM bridge_logs FINAL
			WHERE block_time >= %s AND block_time < %s
			  AND topic0 = unhex('%s') AND address IN (%s)
		)
		WHERE price > 0
		GROUP BY feed, hour
	`, quoted(aggs), quoted(feeds), timeLit(from), timeLit(to), topicAnswerUpdated, unhexList(aggs...))
}

// dexPricesSQL fills price_hourly for hours before Chainlink from Pangolin V2
// and LFJ V1 swaps between a feed's token (ETH, WBTC, LINK, WAVAX, bridged or
// not) and a USD stablecoin: the hour's median price over swaps of at least
// $100. answered_at is the hour's start, so a Chainlink answer in the same
// hour wins.
func dexPricesSQL(from, to time.Time) string {
	var ft, ff, fd, lt, ld []string
	for _, p := range pricedTokens {
		ft = append(ft, "'"+p.Token+"'")
		ff = append(ff, "'"+p.Feed+"'")
		fd = append(fd, fmt.Sprintf("toUInt8(%d)", p.Decimals))
	}
	for _, t := range legacyUSD {
		lt = append(lt, "'"+t.Token+"'")
		ld = append(ld, fmt.Sprintf("toUInt8(%d)", t.Decimals))
	}
	w := func(n int) string {
		return fmt.Sprintf("toFloat64(reinterpretAsUInt256(reverse(substring(l.data, %d, 32))))", 32*n+1)
	}
	return fmt.Sprintf(`
		INSERT INTO price_hourly (feed, hour, price, answered_at)
		WITH
			(SELECT groupArray((lower(hex(token)), decimals)) FROM stablecoins FINAL WHERE chain_id = %[1]d AND peg = 'USD') AS st,
			arrayConcat(arrayMap(x -> x.1, st), [%[2]s]) AS stok,
			arrayConcat(arrayMap(x -> x.2, st), [%[3]s]) AS sdec,
			[%[4]s] AS ftok, [%[5]s] AS ffeed, [%[6]s] AS fdec
		SELECT feed, hour, median(px), toDateTime64(hour, 3, 'UTC')
		FROM (
			SELECT toStartOfHour(l.block_time) AS hour,
				lower(hex(p.token0)) AS t0, lower(hex(p.token1)) AS t1,
				greatest(%[7]s, %[9]s) AS a0, greatest(%[8]s, %[10]s) AS a1,
				transform(t0, ftok, ffeed, '') AS f0, transform(t1, ftok, ffeed, '') AS f1,
				transform(t0, stok, sdec, toUInt8(255)) AS s0, transform(t1, stok, sdec, toUInt8(255)) AS s1,
				multiIf(f0 != '' AND s1 != 255, f0, f1 != '' AND s0 != 255, f1, '') AS feed,
				multiIf(feed = f0 AND f0 != '', a1 / pow(10, s1), a0 / pow(10, s0)) AS usd,
				multiIf(feed = f0 AND f0 != '', a0 / pow(10, transform(t0, ftok, fdec, toUInt8(0))),
					a1 / pow(10, transform(t1, ftok, fdec, toUInt8(0)))) AS amt,
				usd / amt AS px
			FROM raw_logs l
			INNER JOIN (SELECT pool, token0, token1 FROM dex_pools FINAL WHERE dex IN ('pangolin-v2', 'lfj-v1')) p ON l.address = p.pool
			WHERE l.chain_id = %[1]d AND l.block_time >= %[11]s AND l.block_time < %[12]s
			  AND l.topic0 = unhex('%[13]s') AND length(l.data) >= 128
		)
		WHERE feed != '' AND usd >= 100 AND amt > 0 AND isFinite(px)
		GROUP BY feed, hour
	`, ChainID, strings.Join(lt, ", "), strings.Join(ld, ", "), strings.Join(ft, ", "), strings.Join(ff, ", "), strings.Join(fd, ", "),
		w(0), w(1), w(2), w(3), timeLit(from), timeLit(to), topicSwapV2)
}

// chainArrays renders an id -> chain key map as two SQL arrays, in key order.
func chainArrays[K uint32 | uint64](m map[K]string) (string, string) {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	ids := make([]string, len(keys))
	names := make([]string, len(keys))
	for i, k := range keys {
		ids[i] = fmt.Sprint(k)
		names[i] = "'" + m[k] + "'"
	}
	return "CAST([" + strings.Join(ids, ", ") + "] AS Array(UInt64))", "[" + strings.Join(names, ", ") + "]"
}

// deriveSQL decodes the window's legs from bridge_logs and writes them to
// bridge_transfers. See the package comment for the counting rules.
//
// Checks per bridge, besides the contract addresses:
//   - CCTP inflows take the source domain from the MessageReceived the
//     MessageTransmitter logs right after the mint.
//   - LayerZero V2 OFT events count only from valid contracts (bridge_contracts)
//     and only when EndpointV2 logged the same packet in the tx: PacketSent
//     with the OFT as sender and the same guid, or PacketDelivered to the OFT.
//     Stargate pools are exempt: they are fixed addresses, and their packets
//     come from Stargate's TokenMessaging, in a later tx for bus rides.
//     V1 OFT events need a V1 message library log in the tx.
//   - CCIP pool events count only from pools the TokenAdminRegistry lists.
//   - Avalanche Bridge mints count only with an origin tx id, which leaves out
//     BTC.b fee mints. BTC.b burns count as withdrawals only next to a Lombard
//     redeemForBtc request.
func deriveSQL(from, to time.Time) string {
	cctpIDs, cctpKeys := chainArrays(cctpChains)
	lzIDs, lzKeys := chainArrays(lzChains)
	ccipIDs, ccipKeys := chainArrays(ccipChains)
	aebIDs, aebKeys := chainArrays(aebChains)

	var pt, pf, pd []string
	for _, p := range pricedTokens {
		pt = append(pt, "'"+p.Token+"'")
		pf = append(pf, "'"+p.Feed+"'")
		pd = append(pd, fmt.Sprintf("toUInt8(%d)", p.Decimals))
	}
	for _, t := range legacyUSD {
		pt = append(pt, "'"+t.Token+"'")
		pf = append(pf, "'USD'")
		pd = append(pd, fmt.Sprintf("toUInt8(%d)", t.Decimals))
	}

	w := func(n int) string { // uint256 data word n, from 0
		return fmt.Sprintf("reinterpretAsUInt256(reverse(substring(data, %d, 32)))", 32*n+1)
	}
	wa := func(n int) string { // address in data word n
		return fmt.Sprintf("toFixedString(substring(data, %d, 20), 20)", 32*n+13)
	}
	ta := func(n int) string { // address in topic n
		return fmt.Sprintf("toFixedString(substring(assumeNotNull(topic%d), 13, 20), 20)", n)
	}
	tu := func(n int) string { // uint in topic n
		return fmt.Sprintf("reinterpretAsUInt256(reverse(assumeNotNull(topic%d)))", n)
	}
	window := fmt.Sprintf("block_time >= %s AND block_time < %s", timeLit(from), timeLit(to))
	logs := "(SELECT * FROM bridge_logs FINAL WHERE " + window + ")"
	zeroAddr := "toFixedString(unhex('" + strings.Repeat("00", 20) + "'), 20)"
	lzChain := func(id string) string {
		return fmt.Sprintf("transform(toUInt64(%[1]s), %[2]s, %[3]s, concat('lz:', toString(%[1]s)))", id, lzIDs, lzKeys)
	}

	// Each leg: block_time, tx, li, bridge, dir (1 in, 2 out), remote_id,
	// chain, token, amount, from_addr, to_addr.
	legs := []string{
		// CCTP outflows.
		fmt.Sprintf(`SELECT block_time, transaction_hash AS tx, log_index AS li, 'cctp' AS bridge, 2 AS dir,
			concat('cctp:', toString(dom)) AS remote_id,
			transform(dom, %[1]s, %[2]s, concat('cctp:', toString(dom))) AS chain,
			if(topic0 = unhex('%[3]s'), %[4]s, %[5]s) AS token, %[6]s AS amount,
			if(topic0 = unhex('%[3]s'), %[7]s, %[4]s) AS from_addr, %[8]s AS to_addr
		FROM (SELECT *, toUInt64(%[9]s) AS dom FROM %[10]s
			WHERE (address = unhex('%[11]s') AND topic0 = unhex('%[3]s'))
			   OR (address = unhex('%[12]s') AND topic0 = unhex('%[13]s')))`,
			cctpIDs, cctpKeys, topicCCTPDepositV1, ta(2), ta(1), w(0), ta(3), zeroAddr, w(2), logs,
			cctpTokenMessengerV1, cctpTokenMessengerV2, topicCCTPDepositV2),
		// CCTP inflows, with the source domain of the next MessageReceived of
		// the same version.
		fmt.Sprintf(`SELECT m.block_time, m.transaction_hash AS tx, m.log_index AS li, 'cctp' AS bridge, 1 AS dir,
			if(r.ok = 1, concat('cctp:', toString(r.dom)), 'cctp:unknown') AS remote_id,
			if(r.ok = 1, transform(r.dom, %[1]s, %[2]s, concat('cctp:', toString(r.dom))), 'cctp:unknown') AS chain,
			m.token, m.amount, %[3]s AS from_addr, m.to_addr
		FROM (SELECT block_time, transaction_hash, log_index, address = unhex('%[4]s') AS v2,
				%[5]s AS token, %[6]s AS amount, %[7]s AS to_addr
			FROM %[8]s
			WHERE (address = unhex('%[9]s') AND topic0 = unhex('%[10]s'))
			   OR (address = unhex('%[4]s') AND topic0 = unhex('%[11]s'))) m
		ASOF LEFT JOIN (SELECT transaction_hash, address = unhex('%[12]s') AS v2, log_index, toUInt64(%[13]s) AS dom, toUInt8(1) AS ok
			FROM %[8]s
			WHERE (address = unhex('%[14]s') AND topic0 = unhex('%[15]s'))
			   OR (address = unhex('%[12]s') AND topic0 = unhex('%[16]s'))) r
		ON m.transaction_hash = r.transaction_hash AND m.v2 = r.v2 AND r.log_index > m.log_index`,
			cctpIDs, cctpKeys, zeroAddr, cctpTokenMessengerV2, ta(2), w(0), ta(1), logs,
			cctpTokenMessengerV1, topicCCTPMintV1, topicCCTPMintV2,
			cctpMessageTransmitterV2, w(0), cctpMessageTransmitterV1, topicCCTPReceivedV1, topicCCTPReceivedV2),
		// LayerZero V2 outflows: OFTSent paired with EndpointV2's PacketSent.
		fmt.Sprintf(`SELECT l.block_time, l.transaction_hash AS tx, l.log_index AS li, c.bridge, 2 AS dir,
			concat('lz:', toString(eid)) AS remote_id, %[1]s AS chain,
			c.token, %[2]s AS amount, %[3]s AS from_addr, %[4]s AS to_addr
		FROM (SELECT *, toUInt64(%[5]s) AS eid FROM %[6]s WHERE topic0 = unhex('%[7]s') AND length(data) >= 96) l
		INNER JOIN (SELECT address, bridge, token FROM bridge_contracts FINAL WHERE valid AND bridge IN ('layerzero', 'stargate')) c
			ON l.address = c.address
		WHERE c.bridge = 'stargate' OR (l.transaction_hash, assumeNotNull(l.topic1), l.address) IN (
			SELECT transaction_hash,
				toFixedString(substring(data, ps + 81, 32), 32),
				toFixedString(substring(data, ps + 25, 20), 20)
			FROM (SELECT *, toUInt32(%[8]s) + 33 AS ps FROM %[6]s
				WHERE address = unhex('%[9]s') AND topic0 = unhex('%[10]s')))`,
			lzChain("eid"), w(1), ta(2), zeroAddr, w(0), logs, topicOFTSent, w(0), lzEndpointV2, topicPacketSent),
		// LayerZero V2 inflows: OFTReceived with EndpointV2's PacketDelivered
		// to the OFT.
		fmt.Sprintf(`SELECT l.block_time, l.transaction_hash AS tx, l.log_index AS li, c.bridge, 1 AS dir,
			concat('lz:', toString(eid)) AS remote_id, %[1]s AS chain,
			c.token, %[2]s AS amount, %[3]s AS from_addr, %[4]s AS to_addr
		FROM (SELECT *, toUInt64(%[5]s) AS eid FROM %[6]s WHERE topic0 = unhex('%[7]s') AND length(data) >= 64) l
		INNER JOIN (SELECT address, bridge, token FROM bridge_contracts FINAL WHERE valid AND bridge IN ('layerzero', 'stargate')) c
			ON l.address = c.address
		WHERE c.bridge = 'stargate' OR (l.transaction_hash, l.address) IN (
			SELECT transaction_hash, %[8]s FROM %[6]s
			WHERE address = unhex('%[9]s') AND topic0 = unhex('%[10]s') AND length(data) >= 128)`,
			lzChain("eid"), w(1), zeroAddr, ta(2), w(0), logs, topicOFTReceived, wa(3), lzEndpointV2, topicPacketDelivered),
		// LayerZero V1, both directions: the chain id is topic1 and the amount
		// the only data word.
		fmt.Sprintf(`SELECT l.block_time, l.transaction_hash AS tx, l.log_index AS li, c.bridge,
			if(topic0 IN (unhex('%[1]s'), unhex('%[2]s')), 2, 1) AS dir,
			concat('lz:', toString(eid)) AS remote_id, %[3]s AS chain,
			c.token, %[4]s AS amount,
			if(dir = 2, %[5]s, %[6]s) AS from_addr,
			multiIf(dir = 2, %[6]s, topic0 = unhex('%[7]s'), %[8]s, %[9]s) AS to_addr
		FROM (SELECT *, toUInt64(%[10]s) AS eid FROM %[11]s
			WHERE length(data) = 32 AND (
				(topic0 IN (unhex('%[1]s'), unhex('%[2]s'), unhex('%[7]s')) AND topic3 IS NOT NULL)
				OR (topic0 = unhex('%[12]s') AND topic2 IS NOT NULL AND topic3 IS NULL))) l
		INNER JOIN (SELECT address, bridge, token FROM bridge_contracts FINAL WHERE valid AND bridge IN ('layerzero', 'stargate')) c
			ON l.address = c.address
		WHERE l.transaction_hash IN (SELECT transaction_hash FROM %[11]s WHERE address IN (%[13]s))`,
			topicLZ1Send, topicLZ1SendBytes, lzChain("eid"), w(0), ta(2), zeroAddr, topicLZ1ReceiveBytes, ta(3), ta(2),
			tu(1), logs, topicLZ1Receive, unhexList(lzV1Libraries...)),
		// CCIP pools, both directions.
		fmt.Sprintf(`SELECT l.block_time, l.transaction_hash AS tx, l.log_index AS li, 'ccip' AS bridge,
			if(topic0 = unhex('%[1]s'), 2, 1) AS dir,
			concat('ccip:', toString(sel)) AS remote_id,
			transform(sel, %[2]s, %[3]s, concat('ccip:', toString(sel))) AS chain,
			%[4]s AS token, if(dir = 2, %[5]s, %[6]s) AS amount,
			if(dir = 2, %[7]s, %[8]s) AS from_addr, if(dir = 2, %[8]s, %[9]s) AS to_addr
		FROM (SELECT *, toUInt64(%[10]s) AS sel FROM %[11]s
			WHERE (topic0 = unhex('%[1]s') AND length(data) >= 96)
			   OR (topic0 = unhex('%[12]s') AND length(data) >= 128)) l
		WHERE l.address IN (SELECT address FROM bridge_contracts FINAL WHERE valid AND bridge = 'ccip')`,
			topicCCIPLockedOrBurned, ccipIDs, ccipKeys, wa(0), w(2), w(3), wa(1), zeroAddr, wa(2),
			tu(1), logs, topicCCIPReleasedOrMinted),
		// Avalanche Bridge deposits: .e mints from Ethereum and BTC.b mints
		// from Bitcoin, origin tx id required.
		fmt.Sprintf(`SELECT block_time, transaction_hash AS tx, log_index AS li, 'avalanche-bridge' AS bridge, 1 AS dir,
			if(btc, 'ab:bitcoin', 'ab:ethereum') AS remote_id, if(btc, 'bitcoin', 'ethereum') AS chain,
			address AS token, %[1]s AS amount, %[2]s AS from_addr, %[3]s AS to_addr
		FROM (SELECT *, address = unhex('%[4]s') AND topic0 = unhex('%[5]s') AS btc FROM %[6]s
			WHERE ((address = unhex('%[4]s') AND topic0 = unhex('%[5]s') AND length(data) >= 192)
				OR (topic0 = unhex('%[7]s') AND length(data) >= 160
					AND address IN (SELECT address FROM bridge_contracts FINAL WHERE valid AND bridge = 'avalanche-bridge')))
			  AND substring(data, 129, 32) != unhex('%[8]s'))`,
			w(1), zeroAddr, wa(0), BTCb, topicBTCbMint, logs, topicABMint, zero32),
		// Avalanche Bridge withdrawals: Unwrap on a bridge token, chain id 0
		// being the token's home chain, and BTC.b burns next to a Lombard
		// redeemForBtc request.
		fmt.Sprintf(`SELECT block_time, transaction_hash AS tx, log_index AS li, 'avalanche-bridge' AS bridge, 2 AS dir,
			multiIf(address = unhex('%[1]s'), 'ab:bitcoin', cid = 0, 'ab:ethereum', concat('ab:', toString(cid))) AS remote_id,
			multiIf(address = unhex('%[1]s'), 'bitcoin', cid = 0, 'ethereum', concat('ab:', toString(cid))) AS chain,
			address AS token, %[2]s AS amount, tx_from AS from_addr, %[3]s AS to_addr
		FROM (SELECT *, %[4]s AS cid FROM %[5]s
			WHERE topic0 = unhex('%[6]s') AND length(data) = 64
			  AND (address = unhex('%[1]s')
				OR address IN (SELECT address FROM bridge_contracts FINAL WHERE valid AND bridge = 'avalanche-bridge')))
		UNION ALL
		SELECT block_time, transaction_hash AS tx, log_index AS li, 'avalanche-bridge' AS bridge, 2 AS dir,
			'ab:bitcoin' AS remote_id, 'bitcoin' AS chain,
			address AS token, %[2]s AS amount, %[7]s AS from_addr, %[3]s AS to_addr
		FROM %[5]s
		WHERE address = unhex('%[1]s') AND topic0 = unhex('%[8]s') AND topic2 = unhex('%[9]s') AND length(data) = 32
		  AND transaction_hash IN (SELECT transaction_hash FROM %[5]s
			WHERE address = unhex('%[10]s') AND topic0 = unhex('%[11]s') AND substring(data, %[12]d, 4) = unhex('%[13]s'))`,
			BTCb, w(0), zeroAddr, w(1), logs, topicABUnwrap, ta(1), topicTransfer, zero32,
			lombardMailbox, topicMailboxSent, redeemSelectorAt, selectorRedeem),
		// AEB: a token mint in a tx where the Bridge executed a proposal, or a
		// burn in a tx where it took a deposit. The chain id is topic1 of the
		// Bridge event.
		fmt.Sprintf(`SELECT l.block_time, l.transaction_hash AS tx, l.log_index AS li, 'aeb' AS bridge,
			if(l.topic1 = unhex('%[1]s'), 1, 2) AS dir,
			concat('chainbridge:', toString(b.cid)) AS remote_id,
			transform(b.cid, %[2]s, %[3]s, concat('chainbridge:', toString(b.cid))) AS chain,
			l.address AS token, %[4]s AS amount,
			if(dir = 2, %[5]s, %[6]s) AS from_addr, if(dir = 1, %[7]s, %[6]s) AS to_addr
		FROM (SELECT * FROM %[8]s WHERE topic0 = unhex('%[9]s') AND length(data) = 32
			AND (topic1 = unhex('%[1]s') OR topic2 = unhex('%[1]s'))) l
		INNER JOIN (SELECT transaction_hash, any(toUInt64(%[10]s)) AS cid,
				max(topic0 = unhex('%[11]s')) AS dep,
				max(topic0 = unhex('%[12]s') AND %[13]s = %[14]d) AS exe
			FROM %[8]s WHERE address = unhex('%[15]s') AND topic0 IN (unhex('%[11]s'), unhex('%[12]s'))
			GROUP BY transaction_hash) b
		ON l.transaction_hash = b.transaction_hash
		WHERE (l.topic1 = unhex('%[1]s') AND b.exe = 1) OR (l.topic2 = unhex('%[1]s') AND b.dep = 1)`,
			zero32, aebIDs, aebKeys, w(0), ta(1), zeroAddr, ta(2), logs, topicTransfer,
			tu(1), topicAEBDeposit, topicAEBProposal, tu(3), aebExecuted, aebBridge),
	}

	return fmt.Sprintf(`
		INSERT INTO bridge_transfers (block_time, transaction_hash, log_index, bridge, direction, chain, remote_id,
			token, amount, usd, from_address, to_address, via, passthrough)
		WITH
			(SELECT groupArray((lower(hex(token)), decimals)) FROM stablecoins FINAL WHERE chain_id = %[1]d AND peg = 'USD') AS st,
			arrayConcat(arrayMap(x -> x.1, st), [%[2]s]) AS pt,
			arrayConcat(arrayMap(x -> 'USD', st), [%[3]s]) AS pf,
			arrayConcat(arrayMap(x -> x.2, st), [%[4]s]) AS pd
		SELECT block_time, tx, li, bridge, CAST(dir AS Enum8('in' = 1, 'out' = 2)), chain, remote_id, token, amount,
			multiIf(feed = '', NULL,
				feed = 'USD', toFloat64(amount) / pow(10, decs),
				h.px > 0 AND h.px_hour >= bt - INTERVAL %[5]d DAY, toFloat64(amount) / pow(10, decs) * h.px,
				NULL) AS usd,
			from_addr, to_addr, via, passthrough
		FROM (
			SELECT *, toDateTime(block_time) AS bt,
				transform(lower(hex(token)), pt, pf, '') AS feed,
				transform(lower(hex(token)), pt, pd, toUInt8(0)) AS decs,
				max(dir = 1) OVER (PARTITION BY tx) AND max(dir = 2) OVER (PARTITION BY tx) AS passthrough
			FROM (
				SELECT *,
					max(bridge = 'cctp') OVER (PARTITION BY tx, dir) AS has_cctp,
					if(bridge = 'cctp', anyIf(bridge, bridge != 'cctp' AND token = unhex('%[6]s')) OVER (PARTITION BY tx, dir), '') AS via
				FROM (%[7]s)
			)
			WHERE NOT (bridge != 'cctp' AND token = unhex('%[6]s') AND has_cctp)
		) x
		ASOF LEFT JOIN (SELECT feed AS pfeed, hour AS px_hour, price AS px FROM price_hourly FINAL) h
			ON x.feed = h.pfeed AND x.bt >= h.px_hour
	`, ChainID, strings.Join(pt, ", "), strings.Join(pf, ", "), strings.Join(pd, ", "), staleDays, USDC,
		strings.Join(legs, "\nUNION ALL\n"))
}
