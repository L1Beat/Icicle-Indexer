package chwrapper

import (
	"strings"
	"testing"
)

// splitStatements mirrors the strip-then-split logic in ExecuteSql so we can assert the
// embedded DDL parses into clean statements without needing a live ClickHouse.
func splitStatements(sql string) []string {
	var cleaned []string
	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") || trimmed == "" {
			continue
		}
		cleaned = append(cleaned, line)
	}
	var stmts []string
	for _, stmt := range strings.Split(strings.Join(cleaned, "\n"), ";") {
		if s := strings.TrimSpace(stmt); s != "" {
			stmts = append(stmts, s)
		}
	}
	return stmts
}

// TestRawTablesSQLStatementsBalanced guards against a ';' inside a trailing inline comment
// splitting a CREATE TABLE in two (ExecuteSql strips only own-line comments before
// splitting on ';'). Each resulting statement must have balanced parentheses.
func TestRawTablesSQLStatementsBalanced(t *testing.T) {
	for _, src := range []struct {
		name string
		sql  string
	}{
		{"raw_tables.sql", rawTablesSQL},
		{"stablecoins_seed.sql", stablecoinsSeedSQL},
	} {
		stmts := splitStatements(src.sql)
		if len(stmts) == 0 {
			t.Errorf("%s: no statements parsed", src.name)
		}
		for _, s := range stmts {
			if strings.Count(s, "(") != strings.Count(s, ")") {
				head := s
				if len(head) > 100 {
					head = head[:100]
				}
				t.Errorf("%s: unbalanced parentheses in statement (likely a ';' inside an inline comment): %q",
					src.name, strings.ReplaceAll(head, "\n", " "))
			}
		}
	}
}
