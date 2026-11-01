package api

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func countingLoader(calls *atomic.Int32, out []Stablecoin, err error) stablecoinLoader {
	return func(ctx context.Context, chainID uint32) ([]Stablecoin, error) {
		calls.Add(1)
		return out, err
	}
}

func TestStablecoinCache_FreshHitSkipsLoad(t *testing.T) {
	c := newStablecoinCache(time.Minute)
	var calls atomic.Int32
	load := countingLoader(&calls, []Stablecoin{{Symbol: "USDC"}}, nil)

	for i := 0; i < 3; i++ {
		got, err := c.get(context.Background(), 43114, load)
		require.NoError(t, err)
		assert.Equal(t, "USDC", got[0].Symbol)
	}
	assert.Equal(t, int32(1), calls.Load())
}

// A stale copy is served immediately and replaced by one background load.
func TestStablecoinCache_StaleServedWhileRefreshing(t *testing.T) {
	c := newStablecoinCache(time.Minute)
	c.entries[43114] = &stablecoinEntry{data: []Stablecoin{{Symbol: "OLD"}}, fetched: time.Now().Add(-2 * time.Minute)}

	release := make(chan struct{})
	var calls atomic.Int32
	load := func(ctx context.Context, chainID uint32) ([]Stablecoin, error) {
		calls.Add(1)
		<-release
		return []Stablecoin{{Symbol: "NEW"}}, nil
	}

	for i := 0; i < 3; i++ {
		got, err := c.get(context.Background(), 43114, load)
		require.NoError(t, err)
		assert.Equal(t, "OLD", got[0].Symbol)
	}
	close(release)
	assert.Eventually(t, func() bool {
		got, _ := c.get(context.Background(), 43114, load)
		return got[0].Symbol == "NEW"
	}, time.Second, 5*time.Millisecond)
	assert.Equal(t, int32(1), calls.Load())
}

func TestStablecoinCache_ColdRequestsShareOneLoad(t *testing.T) {
	c := newStablecoinCache(time.Minute)
	release := make(chan struct{})
	var calls atomic.Int32
	load := func(ctx context.Context, chainID uint32) ([]Stablecoin, error) {
		calls.Add(1)
		<-release
		return []Stablecoin{{Symbol: "USDC"}}, nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := c.get(context.Background(), 43114, load)
			assert.NoError(t, err)
			assert.Len(t, got, 1)
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	assert.Equal(t, int32(1), calls.Load())
}

// Errors and empty results are not cached: the next request loads again.
func TestStablecoinCache_ErrorsAndEmptyNotCached(t *testing.T) {
	c := newStablecoinCache(time.Minute)
	var calls atomic.Int32

	_, err := c.get(context.Background(), 43114, countingLoader(&calls, nil, errors.New("ch down")))
	assert.Error(t, err)
	got, err := c.get(context.Background(), 99999, countingLoader(&calls, []Stablecoin{}, nil))
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
	assert.Empty(t, c.entries)

	got, err = c.get(context.Background(), 43114, countingLoader(&calls, []Stablecoin{{Symbol: "USDC"}}, nil))
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, int32(3), calls.Load())
}

// Warm-up loads every registry chain, so the first request is a cache hit.
func TestWarmStablecoins(t *testing.T) {
	var queries atomic.Int32
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			queries.Add(1)
			switch {
			case strings.Contains(query, "DISTINCT chain_id"):
				return NewMockRows([]string{"chain_id"}, [][]interface{}{{uint32(43114)}}), nil
			case strings.Contains(query, "SELECT token, symbol"):
				return NewMockRows([]string{"token", "symbol"}, [][]interface{}{{nil, "USDC"}}), nil
			}
			return NewMockRows(nil, nil), nil
		},
	}
	s := NewTestServer(mock)
	s.WarmStablecoins(context.Background())

	require.Eventually(t, func() bool { return queries.Load() == 4 }, time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool {
		s.stablecoins.mu.Lock()
		defer s.stablecoins.mu.Unlock()
		e := s.stablecoins.entries[43114]
		return e != nil && e.data != nil
	}, time.Second, 5*time.Millisecond)

	got, err := s.stablecoins.get(context.Background(), 43114, s.loadStablecoins)
	require.NoError(t, err)
	assert.Equal(t, "USDC", got[0].Symbol)
	assert.Equal(t, int32(4), queries.Load())
}
