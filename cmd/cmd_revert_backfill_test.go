package cmd

import (
	"os"
	"strings"
	"testing"
)

// The recompute must use the live job's INSERT with the anti-join added to both
// trace branches and nothing else changed.
func TestRecomputeInsertSQLFromIndexerFile(t *testing.T) {
	b, err := os.ReadFile("../sql/evm_incremental/native_balances.sql")
	if err != nil {
		t.Fatal(err)
	}
	q, err := recomputeInsertSQL(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(q, "INSERT INTO native_balance_changes") || strings.Contains(q, ";") || strings.Contains(q, "--") {
		t.Fatalf("not a single clean INSERT:\n%s", q)
	}
	if n := strings.Count(q, "NOT IN (\n        SELECT tx_hash, trace_address FROM raw_trace_reverts"); n != 2 {
		t.Fatalf("anti-join applied %d times, want 2", n)
	}
	if !strings.Contains(q, "FROM raw_txs") {
		t.Fatal("gas branch missing")
	}
	if _, err := recomputeInsertSQL("INSERT INTO native_balance_changes SELECT 1"); err == nil {
		t.Fatal("a file without the reverted filters must be rejected")
	}
}
