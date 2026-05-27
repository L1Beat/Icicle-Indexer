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

	statements := strings.Split(sql, ";")

	for _, stmt := range statements {
		// Remove comment lines
		var lines []string
		for _, line := range strings.Split(stmt, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "--") && trimmed != "" {
				lines = append(lines, line)
			}
		}

		cleanStmt := strings.TrimSpace(strings.Join(lines, "\n"))
		if cleanStmt == "" {
			continue
		}

		if err := conn.Exec(ctx, cleanStmt); err != nil {
			return fmt.Errorf("failed to execute statement: %w", err)
		}
	}

	return nil
}
