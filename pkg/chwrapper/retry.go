package chwrapper

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

const (
	MaxRetries     = 5
	InitialBackoff = 100 * time.Millisecond
	MaxBackoff     = 10 * time.Second
)

// isRetryableError checks if the error is a transient connection error
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "acquire conn timeout") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "i/o timeout")
}

// RetryableExec executes a query with retry logic for transient errors
func RetryableExec(ctx context.Context, conn driver.Conn, query string, args ...interface{}) error {
	var lastErr error
	backoff := InitialBackoff

	for attempt := 1; attempt <= MaxRetries; attempt++ {
		err := conn.Exec(ctx, query, args...)
		if err == nil {
			return nil
		}

		if !isRetryableError(err) {
			return err // Non-retryable error, fail immediately
		}

		lastErr = err
		if attempt < MaxRetries {
			log.Printf("[ClickHouse] Retry %d/%d after error: %v (backing off %v)", attempt, MaxRetries, err, backoff)
			time.Sleep(backoff)
			backoff = min(backoff*2, MaxBackoff)
		}
	}

	return lastErr
}

// RetryableQuery executes a query that returns rows with retry logic
func RetryableQuery(ctx context.Context, conn driver.Conn, query string, args ...interface{}) (driver.Rows, error) {
	var lastErr error
	backoff := InitialBackoff

	for attempt := 1; attempt <= MaxRetries; attempt++ {
		rows, err := conn.Query(ctx, query, args...)
		if err == nil {
			return rows, nil
		}

		if !isRetryableError(err) {
			return nil, err
		}

		lastErr = err
		if attempt < MaxRetries {
			log.Printf("[ClickHouse] Retry %d/%d after error: %v (backing off %v)", attempt, MaxRetries, err, backoff)
			time.Sleep(backoff)
			backoff = min(backoff*2, MaxBackoff)
		}
	}

	return nil, lastErr
}

// RetryableQueryRow executes a query that returns a single row
// Note: QueryRow doesn't return error immediately, it's deferred to Scan
// The caller should use WithRetry at a higher level if needed
func RetryableQueryRow(ctx context.Context, conn driver.Conn, query string, args ...interface{}) driver.Row {
	return conn.QueryRow(ctx, query, args...)
}

// WithRetry wraps a function that may fail with retryable errors
func WithRetry(operation func() error) error {
	var lastErr error
	backoff := InitialBackoff

	for attempt := 1; attempt <= MaxRetries; attempt++ {
		err := operation()
		if err == nil {
			return nil
		}

		if !isRetryableError(err) {
			return err
		}

		lastErr = err
		if attempt < MaxRetries {
			log.Printf("[ClickHouse] Retry %d/%d after error: %v (backing off %v)", attempt, MaxRetries, err, backoff)
			time.Sleep(backoff)
			backoff = min(backoff*2, MaxBackoff)
		}
	}

	return lastErr
}
