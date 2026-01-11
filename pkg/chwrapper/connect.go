package chwrapper

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Default pool settings
const (
	DefaultMaxOpenConns = 100
	DefaultMaxIdleConns = 50
	DefaultDialTimeout  = 30 * time.Second
)

// getEnvInt reads an integer from environment variable with a default value
func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultVal
}

func Connect() (driver.Conn, error) {
	// Read pool settings from environment variables
	maxOpenConns := getEnvInt("CH_MAX_OPEN_CONNS", DefaultMaxOpenConns)
	maxIdleConns := getEnvInt("CH_MAX_IDLE_CONNS", DefaultMaxIdleConns)
	dialTimeoutSec := getEnvInt("CH_DIAL_TIMEOUT_SEC", int(DefaultDialTimeout.Seconds()))

	log.Printf("[ClickHouse] Pool config: MaxOpenConns=%d, MaxIdleConns=%d, DialTimeout=%ds",
		maxOpenConns, maxIdleConns, dialTimeoutSec)

	var (
		ctx       = context.Background()
		conn, err = clickhouse.Open(&clickhouse.Options{
			Addr: []string{"127.0.0.1:9000"},
			Auth: clickhouse.Auth{
				Database: "default",
				Username: "default",
				Password: os.Getenv("CLICKHOUSE_PASSWORD"),
			},
			ClientInfo: clickhouse.ClientInfo{
				Products: []struct {
					Name    string
					Version string
				}{
					{Name: "indexer-poc", Version: "0.1"},
				},
			},
			// Connection pool settings - configurable via environment variables:
			// CH_MAX_OPEN_CONNS - Maximum open connections (default: 100)
			// CH_MAX_IDLE_CONNS - Maximum idle connections (default: 50)
			// CH_DIAL_TIMEOUT_SEC - Connection timeout in seconds (default: 30)
			MaxOpenConns:    maxOpenConns,
			MaxIdleConns:    maxIdleConns,
			DialTimeout:     time.Duration(dialTimeoutSec) * time.Second,
			ConnMaxLifetime: 1 * time.Hour, // Recycle connections periodically
		})
	)

	if err != nil {
		return nil, err
	}

	if err := conn.Ping(ctx); err != nil {
		if exception, ok := err.(*clickhouse.Exception); ok {
			fmt.Printf("Exception [%d] %s \n%s\n", exception.Code, exception.Message, exception.StackTrace)
		}
		return nil, err
	}

	log.Printf("[ClickHouse] Connected successfully")
	return conn, nil
}
