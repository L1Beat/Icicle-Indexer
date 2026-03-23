package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/require"
)

// Common test errors
var (
	ErrMockDB      = errors.New("mock database error")
	ErrMockNotImpl = errors.New("not implemented in mock")
)

// MockConn implements a mock ClickHouse connection for testing
type MockConn struct {
	PingFunc     func(ctx context.Context) error
	QueryFunc    func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error)
	QueryRowFunc func(ctx context.Context, query string, args ...interface{}) driver.Row
}

func (m *MockConn) Ping(ctx context.Context) error {
	if m.PingFunc != nil {
		return m.PingFunc(ctx)
	}
	return nil
}

func (m *MockConn) Query(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
	if m.QueryFunc != nil {
		return m.QueryFunc(ctx, query, args...)
	}
	return nil, ErrMockNotImpl
}

func (m *MockConn) QueryRow(ctx context.Context, query string, args ...interface{}) driver.Row {
	if m.QueryRowFunc != nil {
		return m.QueryRowFunc(ctx, query, args...)
	}
	return &MockRow{err: ErrMockNotImpl}
}

// Unused driver.Conn methods - return errors
func (m *MockConn) Stats() driver.Stats                                              { return driver.Stats{} }
func (m *MockConn) Close() error                                                     { return nil }
func (m *MockConn) Exec(ctx context.Context, query string, args ...interface{}) error { return ErrMockNotImpl }
func (m *MockConn) AsyncInsert(ctx context.Context, query string, wait bool, args ...interface{}) error {
	return ErrMockNotImpl
}
func (m *MockConn) PrepareBatch(ctx context.Context, query string, opts ...driver.PrepareBatchOption) (driver.Batch, error) {
	return nil, ErrMockNotImpl
}
func (m *MockConn) ServerVersion() (*driver.ServerVersion, error) { return nil, ErrMockNotImpl }
func (m *MockConn) Contributors() []string                        { return nil }
func (m *MockConn) Select(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	return ErrMockNotImpl
}

// MockRow implements driver.Row for testing
type MockRow struct {
	scanFunc func(dest ...interface{}) error
	err      error
}

func (r *MockRow) Scan(dest ...interface{}) error {
	if r.scanFunc != nil {
		return r.scanFunc(dest...)
	}
	if r.err != nil {
		return r.err
	}
	return nil
}

func (r *MockRow) ScanStruct(dest interface{}) error {
	return ErrMockNotImpl
}

func (r *MockRow) Err() error {
	return r.err
}

// MockRows implements driver.Rows for testing
type MockRows struct {
	data    [][]interface{}
	index   int
	columns []string
	closed  bool
}

func NewMockRows(columns []string, data [][]interface{}) *MockRows {
	return &MockRows{
		columns: columns,
		data:    data,
		index:   -1,
	}
}

func (r *MockRows) Next() bool {
	if r.closed {
		return false
	}
	r.index++
	return r.index < len(r.data)
}

func (r *MockRows) Scan(dest ...interface{}) error {
	if r.index < 0 || r.index >= len(r.data) {
		return errors.New("no row to scan")
	}
	row := r.data[r.index]
	for i, v := range row {
		if i < len(dest) {
			// Simple type assertion - in real tests you'd handle this more carefully
			switch d := dest[i].(type) {
			case *string:
				if s, ok := v.(string); ok {
					*d = s
				}
			case *uint32:
				if n, ok := v.(uint32); ok {
					*d = n
				}
			case *uint64:
				if n, ok := v.(uint64); ok {
					*d = n
				}
			case *int64:
				if n, ok := v.(int64); ok {
					*d = n
				}
			case *bool:
				if b, ok := v.(bool); ok {
					*d = b
				}
			case *float64:
				if f, ok := v.(float64); ok {
					*d = f
				}
			case *time.Time:
				if t, ok := v.(time.Time); ok {
					*d = t
				}
			case **uint32:
				// Handle nullable uint32 pointers
				if ptr, ok := v.(*uint32); ok {
					*d = ptr
				}
			case **uint64:
				// Handle nullable uint64 pointers
				if ptr, ok := v.(*uint64); ok {
					*d = ptr
				}
			case **uint8:
				// Handle nullable uint8 pointers
				if ptr, ok := v.(*uint8); ok {
					*d = ptr
				}
			case **bool:
				// Handle nullable bool pointers
				if ptr, ok := v.(*bool); ok {
					*d = ptr
				}
			case **string:
				// Handle nullable string pointers
				if ptr, ok := v.(*string); ok {
					*d = ptr
				}
			case *[]string:
				// Handle string slices
				if s, ok := v.([]string); ok {
					*d = s
				}
			case *map[string]interface{}:
				// Handle map for tx_data
				if m, ok := v.(map[string]interface{}); ok {
					*d = m
				}
			}
		}
	}
	return nil
}

func (r *MockRows) ScanStruct(dest interface{}) error { return ErrMockNotImpl }
func (r *MockRows) ColumnTypes() []driver.ColumnType  { return nil }
func (r *MockRows) Totals(dest ...interface{}) error  { return nil }
func (r *MockRows) Columns() []string                 { return r.columns }
func (r *MockRows) Close() error {
	r.closed = true
	return nil
}
func (r *MockRows) Err() error { return nil }

// TestServer creates a server with a mock connection for testing
func NewTestServer(mock *MockConn) *Server {
	cfg := Config{
		RateLimit: RateLimitConfig{
			RequestsPerMinute: 1000,      // High limit for tests
			BurstSize:         100,
			CleanupInterval:   time.Hour, // Long interval for tests
		},
	}
	return NewServer(mock, cfg)
}

// MakeRequest makes a test HTTP request and returns the response
func MakeRequest(t *testing.T, server *Server, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	return w
}

// AssertJSONResponse asserts the response is valid JSON with expected status
func AssertJSONResponse(t *testing.T, w *httptest.ResponseRecorder, expectedStatus int) {
	require.Equal(t, expectedStatus, w.Code, "unexpected status code")
	require.Equal(t, "application/json", w.Header().Get("Content-Type"), "expected JSON content type")
}

// ParseResponse parses a JSON response into the given type
func ParseResponse[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	var resp T
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err, "failed to parse response body")
	return resp
}

// AssertErrorResponse asserts an error response with expected code
func AssertErrorResponse(t *testing.T, w *httptest.ResponseRecorder, expectedStatus int, expectedCode ErrorCode) {
	AssertJSONResponse(t, w, expectedStatus)

	var resp ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err, "failed to parse error response")
	require.Equal(t, expectedCode, resp.Error.Code, "unexpected error code")
}

// AssertCORSHeaders asserts CORS headers are present
func AssertCORSHeaders(t *testing.T, w *httptest.ResponseRecorder) {
	require.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}
