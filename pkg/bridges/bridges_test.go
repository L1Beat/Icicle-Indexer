package bridges

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/sha3"
)

func TestBridgeTables(t *testing.T) {
	ids := map[string]bool{}
	for _, b := range Bridges {
		require.False(t, ids[b.ID], b.ID)
		ids[b.ID] = true
	}
	for _, s := range []string{topicCCTPDepositV1, topicOFTSent, topicAnswerUpdated, topicMailboxSent} {
		require.Len(t, s, 64)
	}
	for _, k := range cctpChains {
		require.NotContains(t, k, ":")
	}
	// CCTP, LayerZero and CCIP agree on the big chains' keys.
	require.Equal(t, "ethereum", cctpChains[0])
	require.Equal(t, "ethereum", lzChains[30101])
	require.Equal(t, "ethereum", lzChains[101])
	require.Equal(t, "ethereum", ccipChains[5009297550715157269])
	require.Equal(t, "base", cctpChains[6])
	require.Equal(t, "base", lzChains[30184])
	require.Equal(t, "base", ccipChains[15971525489660198786])
	require.Equal(t, "solana", ccipChains[124615329519749607])
	require.Equal(t, "BNB Chain", ChainName("bsc"))
	require.Equal(t, "lz:30999", ChainName("lz:30999"))
	require.True(t, IsAvalancheL1("dexalot"))
	for _, q := range []string{captureSQL(Start, Start.AddDate(0, 0, 7)), pricesSQL(Start, Start), deriveSQL(Start, Start)} {
		require.NotContains(t, q, "%!")
		require.NotContains(t, q, "?")
	}
}

// fakeCaller answers eth_call by contract and function signature.
type fakeCaller map[string][]byte

func (f fakeCaller) Call(_ context.Context, to string, data []byte) ([]byte, error) {
	for k, v := range f {
		parts := strings.SplitN(k, " ", 2)
		h := sha3.NewLegacyKeccak256()
		h.Write([]byte(parts[1]))
		if "0x"+parts[0] == to && hex.EncodeToString(h.Sum(nil)[:4]) == hex.EncodeToString(data[:4]) {
			if v == nil {
				return nil, errors.New("connection refused")
			}
			return v, nil
		}
	}
	return nil, errors.New("rpc error: execution reverted")
}

func addrRet(a string) []byte { b, _ := hex.DecodeString(strings.Repeat("0", 24) + a); return b }

func strRet(s string) []byte {
	b := make([]byte, 96)
	b[31] = 32
	b[63] = byte(len(s))
	copy(b[64:], s)
	return b
}

func TestContractChecks(t *testing.T) {
	ctx := context.Background()
	const oft, adapter, fake, pool, proxy, tok = "00000000000000000000000000000000000000a1", "00000000000000000000000000000000000000a2",
		"00000000000000000000000000000000000000a3", "00000000000000000000000000000000000000a4", "00000000000000000000000000000000000000a5",
		"00000000000000000000000000000000000000b1"
	c := fakeCaller{
		oft + " endpoint()":                          addrRet(lzEndpointV2),
		oft + " token()":                             addrRet(oft),
		adapter + " endpoint()":                      addrRet(strings.Repeat("0", 40)),
		adapter + " lzEndpoint()":                    addrRet(lzEndpointV1),
		adapter + " innerToken()":                    addrRet(tok),
		fake + " endpoint()":                         addrRet(tok),
		pool + " getToken()":                         addrRet(tok),
		ccipTokenAdminRegistry + " getPool(address)": addrRet(proxy),
		proxy + " getPools()":                        append(append(addrRet(tok), addrRet(pool)...), make([]byte, 64)...),
	}
	got, err := checkLayerZero(ctx, c, oft)
	require.NoError(t, err)
	require.True(t, got.Valid)
	require.Equal(t, oft, got.Token)
	got, err = checkLayerZero(ctx, c, adapter)
	require.NoError(t, err)
	require.True(t, got.Valid)
	require.Equal(t, tok, got.Token)
	got, err = checkLayerZero(ctx, c, fake)
	require.NoError(t, err)
	require.False(t, got.Valid)
	got, err = checkLayerZero(ctx, c, "5634c4a5fed09819e3c46d86a965dd9447d86e47")
	require.NoError(t, err)
	require.Equal(t, "stargate", got.Bridge)

	got, err = checkCCIPPool(ctx, c, pool)
	require.NoError(t, err)
	require.True(t, got.Valid, got.Note)
	require.Equal(t, tok, got.Token)

	be := fakeCaller{
		tok + " symbol()":          strRet("WETH.e"),
		tok + " chainIds(uint256)": addrRet(strings.Repeat("0", 39) + "1"),
		fake + " symbol()":         strRet("SCAM.e"),
	}
	got, err = checkBridgeToken(ctx, be, tok)
	require.NoError(t, err)
	require.True(t, got.Valid)
	got, err = checkBridgeToken(ctx, be, fake)
	require.NoError(t, err)
	require.False(t, got.Valid)

	// A transport failure is an error, so the window is retried.
	_, err = checkLayerZero(ctx, fakeCaller{oft + " endpoint()": nil}, oft)
	require.Error(t, err)
}

// TestDerive_ClickHouseLocal runs the real SQL against synthetic logs in
// clickhouse-local. Set BRIDGES_CH_LOCAL=1 (needs the clickhouse binary).
func TestDerive_ClickHouseLocal(t *testing.T) {
	if os.Getenv("BRIDGES_CH_LOCAL") == "" {
		t.Skip("BRIDGES_CH_LOCAL not set")
	}
	schema, err := os.ReadFile("../chwrapper/raw_tables.sql")
	require.NoError(t, err)
	schema = regexp.MustCompile(`--[^\n]*`).ReplaceAll(schema, nil)
	var ddl []string
	for _, name := range []string{"raw_logs", "stablecoins", "bridge_logs", "bridge_contracts", "price_hourly", "bridge_transfers"} {
		m := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` + name + ` \(.*?;`).Find(schema)
		require.NotNil(t, m, name)
		ddl = append(ddl, string(m))
	}

	const (
		weth     = "49d5c2bdffac6ce2bfdb6640f4f80f226bc10bab" // WETH.e, priced at ETH
		oft      = "00000000000000000000000000000000000000a1" // valid OFT moving WETH.e
		noPacket = "00000000000000000000000000000000000000a2" // valid OFT, but no packet in its tx
		invalid  = "00000000000000000000000000000000000000a3" // failed the on-chain check
		pool     = "00000000000000000000000000000000000000a4" // CCIP pool for USDC
		alice    = "00000000000000000000000000000000000000f1"
		bob      = "00000000000000000000000000000000000000f2"
		guid     = "00000000000000000000000000000000000000000000000000000000000000aa"
	)
	word := func(h string) string { return strings.Repeat("0", 64-len(h)) + h }
	u := func(v string) string {
		n, _ := new(big.Int).SetString(v, 10)
		return fmt.Sprintf("%064x", n)
	}
	n := 0
	row := func(tx, addr, ts, topic0, t1, t2, t3, data string) string {
		nt := func(s string) string {
			if s == "" {
				return "NULL"
			}
			return "unhex('" + word(s) + "')"
		}
		n++
		return fmt.Sprintf("(43114, unhex('%s'), 1, unhex('%s'), '%s', unhex('%s'), 0, %d, unhex('%s'), NULL, unhex('%s'), %s, %s, %s, unhex('%s'), false)",
			addr, strings.Repeat("0", 64), ts, word(tx), n, alice, topic0, nt(t1), nt(t2), nt(t3), data)
	}
	at := "2026-10-01 10:00:00.000"
	packet := func(sender, g string) string { // PacketSent data: offsets, library, then the encoded packet
		p := "01" + strings.Repeat("0", 16) + "0000759a" + word(sender) + "00007595" + word(bob) + g
		p += strings.Repeat("0", (64-len(p)%64)%64)
		return word("60") + word("0") + word("0") + word(fmt.Sprintf("%x", (len(p)/2))) + p
	}
	mailbox := strings.Repeat("00", redeemSelectorAt-1) + selectorRedeem + strings.Repeat("00", 512-redeemSelectorAt-3)
	rows := []string{
		// ETH at $2,000 an hour earlier.
		row("10", "9ff520924dc295074f43a8a01ee30a4d39a4223b", "2026-10-01 09:00:00.000", topicAnswerUpdated, fmt.Sprintf("%x", 2000*100000000), "1", "", word("0")),
		// CCTP V2 out: 1,000 USDC to Base.
		row("a", cctpTokenMessengerV2, at, topicCCTPDepositV2, USDC, alice, "3e8", u("1000000000")+word(bob)+u("6")+word("0")+word("0")+word("0")+word("0")),
		// CCTP V1 in: 500 USDC from Ethereum, domain from the MessageReceived after the mint.
		row("b", cctpTokenMessengerV1, at, topicCCTPMintV1, bob, USDC, "", u("500000000")),
		row("b", cctpMessageTransmitterV1, at, topicCCTPReceivedV1, alice, "1", "", u("0")+word("0")+word("0")),
		// CCIP USDC through CCTP: one leg, CCTP, via ccip.
		row("c", pool, at, topicCCIPLockedOrBurned, fmt.Sprint(new(big.Int).SetUint64(5009297550715157269).Text(16)), "", "", word(USDC)+word(alice)+u("200000000")),
		row("c", cctpTokenMessengerV2, at, topicCCTPDepositV2, USDC, pool, "3e8", u("200000000")+word(bob)+u("0")+word("0")+word("0")+word("0")+word("0")),
		// In and out in one tx: passthrough.
		row("d", cctpTokenMessengerV1, at, topicCCTPMintV1, bob, USDC, "", u("300000000")),
		row("d", cctpMessageTransmitterV1, at, topicCCTPReceivedV1, alice, "2", "", u("3")+word("0")+word("0")),
		row("d", cctpTokenMessengerV2, at, topicCCTPDepositV2, USDC, bob, "3e8", u("300000000")+word(bob)+u("6")+word("0")+word("0")+word("0")+word("0")),
		// LayerZero V2 out: 1 WETH.e to Ethereum with its packet, $2,000.
		row("e", oft, at, topicOFTSent, guid, alice, "", u("30101")+u("1000000000000000000")+u("1000000000000000000")),
		row("e", lzEndpointV2, at, topicPacketSent, "", "", "", packet(oft, guid)),
		// No matching packet, and a contract that failed its check: ignored.
		row("f", noPacket, at, topicOFTSent, guid, alice, "", u("30101")+u("1000000000000000000")+u("1000000000000000000")),
		row("f", lzEndpointV2, at, topicPacketSent, "", "", "", packet(oft, word("bb"))),
		row("1a", invalid, at, topicOFTSent, guid, alice, "", u("30101")+u("1000000000000000000")+u("1000000000000000000")),
		row("1a", lzEndpointV2, at, topicPacketSent, "", "", "", packet(invalid, guid)),
		// BTC.b from Bitcoin (no BTC price yet: unpriced), and a fee mint (no origin tx): ignored.
		row("1b", BTCb, at, topicBTCbMint, "", "", "", word(bob)+u("100000000")+word("0")+word("0")+word("ab")+word("0")),
		row("1b", BTCb, at, topicBTCbMint, "", "", "", word(bob)+u("10000")+word("0")+word("0")+word("0")+word("0")),
		// BTC.b withdrawal to Bitcoin next to a redeemForBtc request, and a plain burn: ignored.
		row("1c", BTCb, at, topicTransfer, alice, "0", "", u("50000000")),
		row("1c", lombardMailbox, at, topicMailboxSent, "1", alice, "2", mailbox),
		row("1d", BTCb, at, topicTransfer, alice, "0", "", u("70000000")),
		// WETH.e unwrapped to Ethereum, 2 WETH.e, $4,000.
		row("1e", weth, at, topicABUnwrap, "", "", "", u("2000000000000000000")+u("0")),
		// Next window: not decoded.
		row("1f", cctpTokenMessengerV2, "2026-10-02 10:00:00.000", topicCCTPDepositV2, USDC, alice, "3e8", u("1")+word(bob)+u("6")+word("0")+word("0")+word("0")+word("0")),
	}
	contracts := fmt.Sprintf(`INSERT INTO bridge_contracts (address, bridge, token, valid, note) VALUES
		(unhex('%s'), 'layerzero', unhex('%s'), true, ''), (unhex('%s'), 'layerzero', unhex('%s'), true, ''),
		(unhex('%s'), 'layerzero', unhex('%s'), false, ''), (unhex('%s'), 'ccip', unhex('%s'), true, ''),
		(unhex('%s'), 'avalanche-bridge', unhex('%s'), true, '');`,
		oft, weth, noPacket, weth, invalid, weth, pool, USDC, weth, weth)

	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	script := strings.Join(ddl, "\n") + "\n" +
		"INSERT INTO stablecoins (chain_id, token, symbol, decimals, peg) VALUES (43114, unhex('" + USDC + "'), 'USDC', 6, 'USD');\n" +
		"INSERT INTO raw_logs VALUES " + strings.Join(rows, ",\n") + ";\n" +
		captureSQL(from, to) + ";\n" + contracts + "\n" + pricesSQL(from, to) + ";\n" + deriveSQL(from, to) + ";\n" +
		"SELECT bridge, direction, chain, toString(amount), ifNull(toString(round(usd, 2)), 'null'), via, passthrough, toBool(lower(hex(from_address)) = '" + alice + "') FROM bridge_transfers FINAL ORDER BY bridge, direction, chain, amount FORMAT TSV;\n"

	f, err := os.CreateTemp(t.TempDir(), "q*.sql")
	require.NoError(t, err)
	_, _ = f.WriteString(script)
	f.Close()
	out, err := exec.Command("clickhouse", "local", "--multiquery", "--queries-file", f.Name()).CombinedOutput()
	require.NoError(t, err, string(out))

	require.Equal(t, []string{
		"avalanche-bridge\tin\tbitcoin\t100000000\tnull\t\tfalse\tfalse",
		"avalanche-bridge\tout\tbitcoin\t50000000\tnull\t\tfalse\ttrue",
		"avalanche-bridge\tout\tethereum\t2000000000000000000\t4000\t\tfalse\ttrue",
		"cctp\tin\tarbitrum\t300000000\t300\t\ttrue\tfalse",
		"cctp\tin\tethereum\t500000000\t500\t\tfalse\tfalse",
		"cctp\tout\tbase\t300000000\t300\t\ttrue\tfalse",
		"cctp\tout\tbase\t1000000000\t1000\t\tfalse\ttrue",
		"cctp\tout\tethereum\t200000000\t200\tccip\tfalse\tfalse",
		"layerzero\tout\tethereum\t1000000000000000000\t2000\t\tfalse\ttrue",
	}, strings.Split(strings.TrimSpace(string(out)), "\n"))
}

// TestAEB_ClickHouseLocal covers the AEB legs and the DEX prices used before
// Chainlink. Set BRIDGES_CH_LOCAL=1 (needs the clickhouse binary).
func TestAEB_ClickHouseLocal(t *testing.T) {
	if os.Getenv("BRIDGES_CH_LOCAL") == "" {
		t.Skip("BRIDGES_CH_LOCAL not set")
	}
	schema, err := os.ReadFile("../chwrapper/raw_tables.sql")
	require.NoError(t, err)
	schema = regexp.MustCompile(`--[^\n]*`).ReplaceAll(schema, nil)
	var ddl []string
	for _, name := range []string{"raw_logs", "stablecoins", "dex_pools", "bridge_logs", "bridge_contracts", "price_hourly", "bridge_transfers"} {
		m := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` + name + ` \(.*?;`).Find(schema)
		require.NotNil(t, m, name)
		ddl = append(ddl, string(m))
	}
	const (
		eth   = "f20d962a6c8f70c731bd838a3a388d7d48fa6e15" // ETH (AEB)
		usdt  = "de3a24028580884448a5397872046a019649b084" // USDT (AEB)
		pair  = "00000000000000000000000000000000000000c1" // Pangolin V2 ETH/USDT
		apair = "00000000000000000000000000000000000000c2" // Pangolin V2 WAVAX/USDT
		lpair = "00000000000000000000000000000000000000c3" // Pangolin V2 LINK/WAVAX
		link  = "b3fe5374f67d7a22886a0ee082b2e2f9d2651651" // LINK (AEB)
		alice = "00000000000000000000000000000000000000f1"
		route = "00000000000000000000000000000000000000e9" // a contract calling the Bridge
	)
	word := func(h string) string { return strings.Repeat("0", 64-len(h)) + h }
	u := func(v string) string {
		n, _ := new(big.Int).SetString(v, 10)
		return fmt.Sprintf("%064x", n)
	}
	n := 0
	row := func(tx, txTo, addr, ts, topic0, t1, t2, t3, data string) string {
		nt := func(s string) string {
			if s == "" {
				return "NULL"
			}
			return "unhex('" + word(s) + "')"
		}
		to := "NULL"
		if txTo != "" {
			to = "unhex('" + txTo + "')"
		}
		n++
		return fmt.Sprintf("(43114, unhex('%s'), 1, unhex('%s'), '%s', unhex('%s'), 0, %d, unhex('%s'), %s, unhex('%s'), %s, %s, %s, unhex('%s'), false)",
			addr, strings.Repeat("0", 64), ts, word(tx), n, alice, to, topic0, nt(t1), nt(t2), nt(t3), data)
	}
	rows := []string{
		// Pangolin V2: 2 ETH in for 3,200 USDT out, then 1 ETH out for 1,700 USDT in: median $1,650.
		row("1", pair, pair, "2021-03-01 09:10:00.000", topicSwapV2, alice, alice, "", u("2000000000000000000")+u("0")+u("0")+u("3200000000")),
		row("2", pair, pair, "2021-03-01 09:20:00.000", topicSwapV2, alice, alice, "", u("0")+u("1700000000")+u("1000000000000000000")+u("0")),
		// AVAX at $20 from WAVAX/USDT, and LINK at 2 AVAX = $40 through its WAVAX pair.
		row("4", apair, apair, "2021-03-01 09:40:00.000", topicSwapV2, alice, alice, "", u("10000000000000000000")+u("0")+u("0")+u("200000000")),
		row("5", lpair, lpair, "2021-03-01 09:50:00.000", topicSwapV2, alice, alice, "", u("5000000000000000000")+u("0")+u("0")+u("10000000000000000000")),
		// $50 swap: below the $100 floor, ignored.
		row("3", pair, pair, "2021-03-01 09:30:00.000", topicSwapV2, alice, alice, "", u("0")+u("50000000")+u("1000000000000000")+u("0")),
		// Inflow: 3 ETH minted on an executed proposal from Ethereum (chain 1): $4,950.
		row("a", aebBridge, eth, "2021-03-01 10:00:00.000", topicTransfer, "0", alice, "", u("3000000000000000000")),
		row("a", aebBridge, aebBridge, "2021-03-01 10:00:00.000", topicAEBProposal, "1", "7", "3", word("0")+word("0")),
		// Outflow: 500 USDT burnt on a deposit to chain 1.
		row("b", aebBridge, usdt, "2021-03-01 11:00:00.000", topicTransfer, alice, "0", "", u("500000000")),
		row("b", aebBridge, aebBridge, "2021-03-01 11:00:00.000", topicAEBDeposit, "1", "aa", "8", ""),
		// Inflow through a contract (tx not sent to the Bridge): 10 LINK, $400.
		row("e", route, link, "2021-03-01 11:30:00.000", topicTransfer, "0", alice, "", u("10000000000000000000")),
		row("e", route, aebBridge, "2021-03-01 11:30:00.000", topicAEBProposal, "1", "a", "3", word("0")+word("0")),
		// A vote that did not execute, and a burn outside the bridge: ignored.
		row("c", aebBridge, eth, "2021-03-01 12:00:00.000", topicTransfer, "0", alice, "", u("1")),
		row("c", aebBridge, aebBridge, "2021-03-01 12:00:00.000", topicAEBProposal, "1", "9", "2", word("0")+word("0")),
		row("d", "", usdt, "2021-03-01 12:00:00.000", topicTransfer, alice, "0", "", u("700000000")),
	}
	from := time.Date(2021, 3, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	script := strings.Join(ddl, "\n") + "\n" +
		"INSERT INTO dex_pools (pool, dex, factory, token0, token1, created_time) VALUES (unhex('" + pair + "'), 'pangolin-v2', unhex('" + pair + "'), unhex('" + eth + "'), unhex('" + usdt + "'), '2021-02-01 00:00:00'), " +
		"(unhex('" + apair + "'), 'pangolin-v2', unhex('" + apair + "'), unhex('" + dexWAVAX + "'), unhex('" + usdt + "'), '2021-02-01 00:00:00'), " +
		"(unhex('" + lpair + "'), 'pangolin-v2', unhex('" + lpair + "'), unhex('" + link + "'), unhex('" + dexWAVAX + "'), '2021-02-01 00:00:00');\n" +
		"INSERT INTO raw_logs VALUES " + strings.Join(rows, ",\n") + ";\n" +
		captureSQL(from, to) + ";\n" + dexPricesSQL(from, to) + ";\n" + dexAvaxPricesSQL(from, to) + ";\n" + pricesSQL(from, to) + ";\n" + deriveSQL(from, to) + ";\n" +
		"SELECT feed, toString(hour), price FROM price_hourly FINAL ORDER BY feed FORMAT TSV;\n" +
		"SELECT bridge, direction, chain, remote_id, lower(hex(token)), toString(amount), ifNull(toString(round(usd, 2)), 'null') FROM bridge_transfers FINAL ORDER BY block_time FORMAT TSV;\n"

	f, err := os.CreateTemp(t.TempDir(), "q*.sql")
	require.NoError(t, err)
	_, _ = f.WriteString(script)
	f.Close()
	out, err := exec.Command("clickhouse", "local", "--multiquery", "--queries-file", f.Name()).CombinedOutput()
	require.NoError(t, err, string(out))
	require.Equal(t, []string{
		"AVAX\t2021-03-01 09:00:00\t20",
		"ETH\t2021-03-01 09:00:00\t1650",
		"LINK\t2021-03-01 09:00:00\t40",
		"aeb\tin\tethereum\tchainbridge:1\t" + eth + "\t3000000000000000000\t4950",
		"aeb\tout\tethereum\tchainbridge:1\t" + usdt + "\t500000000\t500",
		"aeb\tin\tethereum\tchainbridge:1\t" + link + "\t10000000000000000000\t400",
	}, strings.Split(strings.TrimSpace(string(out)), "\n"))
}
