package icm

import (
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/sha3"
)

func TestTopics(t *testing.T) {
	topic := func(sig string) string {
		h := sha3.NewLegacyKeccak256()
		h.Write([]byte(sig))
		return hex.EncodeToString(h.Sum(nil))
	}
	const msg = "(uint256,address,bytes32,address,uint256,address[],(uint256,address)[],bytes)"
	require.Equal(t, topicSend, topic("SendCrossChainMessage(bytes32,bytes32,"+msg+",(address,uint256))"))
	require.Equal(t, topicReceive, topic("ReceiveCrossChainMessage(bytes32,bytes32,address,address,"+msg+")"))
}

// TestCapture_ClickHouseLocal runs captureSQL against the real schema in
// clickhouse-local. Set ICM_CH_LOCAL=1 (needs the clickhouse binary).
func TestCapture_ClickHouseLocal(t *testing.T) {
	if os.Getenv("ICM_CH_LOCAL") == "" {
		t.Skip("ICM_CH_LOCAL not set")
	}
	schema, err := os.ReadFile("../chwrapper/raw_tables.sql")
	require.NoError(t, err)
	schema = regexp.MustCompile(`--[^\n]*`).ReplaceAll(schema, nil)
	var ddl []string
	for _, name := range []string{"raw_logs", "icm_messages"} {
		m := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` + name + ` \(.*?;`).Find(schema)
		require.NotNil(t, m, name)
		ddl = append(ddl, string(m))
	}
	const (
		spoof   = "00000000000000000000000000000000000000e1"
		dexalot = "aa" // destination / source blockchain IDs
		gunz    = "bb"
		// MessageExecuted(bytes32,bytes32), also from the messenger
		topicExecuted = "34795cc6b122b9a0ae684946319f1e14a577b4e8f9b3dda9ac94c21a54d3188c"
	)
	word := func(h string) string { return strings.Repeat("0", 64-len(h)) + h }
	n := 0
	row := func(addr, ts, topic0, t1, t2 string) string {
		n++
		return fmt.Sprintf("(43114, unhex('%s'), %d, unhex('%s'), '%s', unhex('%s'), 0, %d, unhex('%s'), NULL, unhex('%s'), unhex('%s'), unhex('%s'), NULL, '', false)",
			addr, n, word(""), ts, word(fmt.Sprintf("%x", n)), n, strings.Repeat("00", 20), topic0, word(t1), word(t2))
	}
	rows := []string{
		row(Messenger, "2026-11-01 10:00:00", topicSend, "01", dexalot),
		row(Messenger, "2026-11-01 11:00:00", topicSend, "02", gunz),
		row(Messenger, "2026-11-01 12:00:00", topicReceive, "03", dexalot),
		row(Messenger, "2026-11-01 12:00:00", topicExecuted, "03", dexalot), // not a send or receive
		row(spoof, "2026-11-01 13:00:00", topicSend, "04", dexalot),         // not the messenger
		row(Messenger, "2026-11-03 00:00:00", topicSend, "05", dexalot),     // past the window
	}
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	q := strings.Join(ddl, "\n") + "\n" +
		"INSERT INTO raw_logs VALUES " + strings.Join(rows, ",\n") + ";\n" +
		captureSQL(from, to) + ";\n" +
		captureSQL(from, to) + ";\n" + // a replayed window adds nothing
		`SELECT direction, lower(hex(message_id)), lower(hex(counterparty)) FROM icm_messages FINAL
		 ORDER BY direction, message_id FORMAT TSV;`
	f, err := os.CreateTemp(t.TempDir(), "icm-*.sql")
	require.NoError(t, err)
	_, err = f.WriteString(q)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	out, err := exec.Command("clickhouse", "local", "--multiquery", "--queries-file", f.Name()).CombinedOutput()
	require.NoError(t, err, string(out))
	require.Equal(t, strings.Join([]string{
		"sent\t" + word("01") + "\t" + word(dexalot),
		"sent\t" + word("02") + "\t" + word(gunz),
		"received\t" + word("03") + "\t" + word(dexalot),
	}, "\n")+"\n", string(out))
}
