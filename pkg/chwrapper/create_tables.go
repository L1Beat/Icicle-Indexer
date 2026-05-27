package chwrapper

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

//go:embed raw_tables.sql
var rawTablesSQL string

//go:embed stablecoins_seed.sql
var stablecoinsSeedSQL string

func CreateTables(conn driver.Conn) error {
	if err := ExecuteSql(conn, rawTablesSQL); err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}
	if err := ExecuteSql(conn, stablecoinsSeedSQL); err != nil {
		return fmt.Errorf("failed to seed stablecoins: %w", err)
	}
	return nil
}

func ExecuteSql(conn driver.Conn, sql string) error {
	ctx := context.Background()

	// Strip line comments first, then split on ; — otherwise a ';' inside a
	// comment splits a statement in two and the second half becomes garbage.
	var cleaned []string
	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") || trimmed == "" {
			continue
		}
		cleaned = append(cleaned, line)
	}

	for _, stmt := range strings.Split(strings.Join(cleaned, "\n"), ";") {
		cleanStmt := strings.TrimSpace(stmt)
		if cleanStmt == "" {
			continue
		}
		if err := conn.Exec(ctx, cleanStmt); err != nil {
			return fmt.Errorf("failed to execute statement: %w", err)
		}
	}

	return nil
}
