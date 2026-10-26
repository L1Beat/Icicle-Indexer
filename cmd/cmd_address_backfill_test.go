package cmd

import (
	"os"
	"strings"
	"testing"
)

// The backfill must copy rows exactly as the materialized views do, or history
// and live rows would differ.
func TestAddressBackfillMatchesViews(t *testing.T) {
	schema, err := os.ReadFile("../pkg/chwrapper/raw_tables.sql")
	if err != nil {
		t.Fatal(err)
	}
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	for name, sel := range addressBackfillSelects {
		view := "CREATE MATERIALIZED VIEW IF NOT EXISTS " + sel.target + "_mv TO " + sel.target + " AS " + norm(sel.sql)
		if !strings.Contains(norm(string(schema)), view) {
			t.Errorf("%s: backfill select does not match %s_mv in raw_tables.sql", name, sel.target)
		}
	}
}
