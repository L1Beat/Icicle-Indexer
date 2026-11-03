package api

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestICMTransfersSQL_ClickHouseLocal runs icmTransfersSQL against the real
// schema in clickhouse-local. Set ICM_CH_LOCAL=1 (needs the clickhouse binary).
func TestICMTransfersSQL_ClickHouseLocal(t *testing.T) {
	if os.Getenv("ICM_CH_LOCAL") == "" {
		t.Skip("ICM_CH_LOCAL not set")
	}
	schema, err := os.ReadFile("../chwrapper/raw_tables.sql")
	require.NoError(t, err)
	schema = regexp.MustCompile(`--[^\n]*`).ReplaceAll(schema, nil)
	var ddl []string
	for _, name := range []string{"icm_messages", "icm_executions", "icm_apps", "icm_remotes", "stablecoins", "price_hourly"} {
		m := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` + name + ` \(.*?;`).Find(schema)
		require.NotNil(t, m, name)
		ddl = append(ddl, string(m))
	}
	const (
		xom      = "af72f6caf76775daeabaf5c82ce05d97359c8fa1" // TokenRemote, unpriced
		usdcHome = "4ef4752d1a4a6bb1fa7bc862ba646643c7f30ed3"
		usdc     = "b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"
		avaxHome = "0dd3b79bb269c06cf851676214bc8008c5ee09ef"
		wavax    = "b31f66aa3c1e785363f0875a1b74e27b85fd66c7"
		fakeHome = "33a31e0f62c0ddf25090b61ef21a70d5f48725b7"
		fake     = "0cee9001dafff03080e71b76c47e2c89c4c9e752" // calls itself WAVAX
		other    = "a8baad3115a133b101ef935cb2e198fd04f1c659"
		rem      = "00000000000000000000000000000000000000r1"
	)
	chainA, chainB := strings.Repeat("aa", 32), strings.Repeat("bb", 32)
	n := 0
	msg := func(dir, ts, app, chain, remote string, typ int, amount string, tx string) string {
		n++
		return fmt.Sprintf("('%s', unhex('%064x'), unhex('%s'), '%s', %d, unhex('%s'), %d, unhex('%s'), unhex('%s'), 0, unhex('%s'), 0, unhex('%s'), unhex('%s'), %d, unhex('%s'), %s)",
			dir, n, chain, ts, n, strings.Repeat(tx, 32), n, app, strings.ReplaceAll(remote, "r", "0"),
			strings.Repeat("00", 20), strings.Repeat("00", 20), strings.Repeat("00", 20), typ, strings.Repeat("c5", 20), amount)
	}
	rows := []string{
		msg("received", "2026-11-01 10:00:00", xom, chainA, rem, 1, "10039886999999999996", "01"),
		msg("sent", "2026-11-01 10:30:00", usdcHome, chainA, rem, 1, "12000000", "02"),
		// multiply_on_remote with a 1e12 multiplier: the home amount is the message amount / 1e12.
		msg("received", "2026-11-01 11:15:00", avaxHome, chainB, rem, 1, "5000000000000000000000000000000", "03"),
		// An unregistered remote: no amount.
		msg("sent", "2026-11-01 12:00:00", usdcHome, chainB, "00000000000000000000000000000000000000ff", 1, "7000000", "04"),
		// Multi-hop through the USDC home: the receive counts, the forwarding send does not.
		msg("received", "2026-11-01 13:00:00", usdcHome, chainA, rem, 3, "3000000", "05"),
		msg("sent", "2026-11-01 13:00:00", usdcHome, chainB, "00000000000000000000000000000000000000ff", 1, "3000000", "05"),
		msg("sent", "2026-11-01 14:00:00", fakeHome, chainA, rem, 1, "1000000000000000000", "06"),
		msg("sent", "2026-11-01 15:00:00", other, chainA, rem, -1, "0", "07"),       // not ICTT
		msg("received", "2026-11-01 16:00:00", usdcHome, chainA, rem, 0, "0", "08"), // register_remote
		// A receive whose execution failed: no MessageExecuted, left out.
		msg("received", "2026-11-01 17:00:00", usdcHome, chainA, rem, 1, "50000000", "0a"),
		msg("sent", "2026-10-20 10:00:00", usdcHome, chainA, rem, 1, "99000000", "09"), // before the window
	}
	q := strings.Join(ddl, "\n") + "\n" +
		"INSERT INTO icm_messages (direction, message_id, counterparty, block_time, block_number, transaction_hash, log_index, local_address, remote_address, required_gas_limit, fee_token, fee_amount, deliverer, reward_redeemer, ictt_type, ictt_recipient, ictt_amount) VALUES " + strings.Join(rows, ",\n") + ";\n" +
		fmt.Sprintf(`INSERT INTO icm_apps (address, kind, token, symbol, decimals, note) VALUES
			(unhex('%s'), 'ictt-remote', unhex('%s'), 'XOM', 18, ''),
			(unhex('%s'), 'ictt-home', unhex('%s'), 'USDC', 6, ''),
			(unhex('%s'), 'ictt-home', unhex('%s'), 'WAVAX', 18, ''),
			(unhex('%s'), 'ictt-home', unhex('%s'), 'WAVAX', 18, ''),
			(unhex('%s'), '', unhex('%s'), '', 0, 'not ICTT');
		INSERT INTO icm_remotes (home, remote_chain, remote_address, registered, multiplier, multiply_on_remote) VALUES
			(unhex('%s'), unhex('%s'), unhex('%s'), true, 1, false),
			(unhex('%s'), unhex('%s'), unhex('%s'), true, 1000000000000, true),
			(unhex('%s'), unhex('%s'), unhex('%s'), true, 1, false);
		INSERT INTO icm_executions (message_id, source, block_time, transaction_hash)
			SELECT message_id, counterparty, block_time, transaction_hash FROM icm_messages
			WHERE direction = 'received' AND transaction_hash != unhex(repeat('0a', 32));
		INSERT INTO stablecoins (chain_id, token, symbol, name, decimals) VALUES (43114, unhex('%s'), 'USDC', 'USD Coin', 6);
		INSERT INTO price_hourly (feed, hour, price, answered_at) VALUES ('AVAX', '2026-11-01 11:00:00', 20, '2026-11-01 11:00:00');
		`, xom, xom, usdcHome, usdc, avaxHome, wavax, fakeHome, fake, other, strings.Repeat("00", 20),
			usdcHome, chainA, strings.ReplaceAll(rem, "r", "0"), avaxHome, chainB, strings.ReplaceAll(rem, "r", "0"),
			fakeHome, chainA, strings.ReplaceAll(rem, "r", "0"), usdc)
	// Bind the four window bounds the way clickhouse-go would.
	from, to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	bind := func(q string, extra ...string) string {
		for _, b := range []time.Time{from, to, from, from, to} {
			q = strings.Replace(q, "?", "toDateTime64('"+b.Format("2006-01-02 15:04:05")+"', 3, 'UTC')", 1)
		}
		for _, e := range extra {
			q = strings.Replace(q, "?", e, 1)
		}
		return q
	}
	q += `SELECT lower(hex(app)), direction, ictt_type, toString(amount), round(usd, 6), priced FROM (` + bind(icmTransfersSQL()) + `)
		ORDER BY block_time, direction FORMAT TSV;
		SELECT '--';
		SELECT * FROM (` + bind(icmValueSQL()) + `) ORDER BY 1, 5 FORMAT TSV;
		SELECT '--';
		SELECT * FROM (` + bind(icmValueDaysSQL()) + `) ORDER BY 1, 2 FORMAT TSV;
		SELECT '--';
		SELECT * FROM (` + bind(icmTopSQL(), "2") + `) FORMAT TSV;`
	dir := t.TempDir()
	f := dir + "/q.sql"
	require.NoError(t, os.WriteFile(f, []byte(q), 0o600))
	out, err := exec.Command("clickhouse", "local", "--path", dir+"/data", "--multiquery", "--queries-file", f).CombinedOutput()
	require.NoError(t, err, string(out))
	parts := strings.Split(string(out), "--\n")
	require.Len(t, parts, 4, string(out))
	require.Equal(t, strings.Join([]string{
		xom + "\treceived\t1\t10039886999999999996\t\\N\tfalse",
		usdcHome + "\tsent\t1\t12000000\t12\ttrue",
		avaxHome + "\treceived\t1\t5000000000000000000\t100\ttrue",
		usdcHome + "\tsent\t1\t\\N\t\\N\ttrue",
		usdcHome + "\treceived\t3\t3000000\t3\ttrue",
		fakeHome + "\tsent\t1\t1000000000000000000\t\\N\tfalse",
	}, "\n")+"\n", parts[0])
	// Totals: 6 transfers, 3 priced, $12 sent and $103 received.
	require.Contains(t, parts[1], "\t6\t3\t1000000000012000000\t15039887000002999996\t12\t103\tfalse\tfalse\n")
	// Per token: real WAVAX priced at the hour's AVAX, the lookalike not.
	require.Contains(t, parts[1], wavax+"\tWAVAX\t18\ttrue\t")
	require.Contains(t, parts[1], fake+"\tWAVAX\t18\tfalse\t")
	require.Contains(t, parts[1], usdc+"\tUSDC\t6\ttrue\t"+strings.Repeat("\\0", 32)+"\t3\t2\t12000000\t3000000\t12\t3\ttrue\tfalse\n")
	require.Contains(t, parts[2], "2026-11-01\t"+usdc+"\tUSDC\ttrue\t3\t12\t3\n")
	top := strings.Split(strings.TrimSpace(parts[3]), "\n")
	require.Len(t, top, 2)
	require.True(t, strings.HasSuffix(top[0], "\t5000000000000000000\t100"), top[0])
	require.True(t, strings.HasSuffix(top[1], "\t12000000\t12"), top[1])
}
