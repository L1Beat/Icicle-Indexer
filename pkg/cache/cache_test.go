package cache

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestCache(t *testing.T) *Cache {
	t.Helper()
	c, err := New(t.TempDir(), 1)
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

func TestFormatBlockKey(t *testing.T) {
	key := formatBlockKey(42)
	assert.Equal(t, []byte("block:00000000000042"), key)

	key0 := formatBlockKey(0)
	assert.Equal(t, []byte("block:00000000000000"), key0)

	keyLarge := formatBlockKey(99999999999999)
	assert.Equal(t, []byte("block:99999999999999"), keyLarge)
}

func TestParseBlockKey(t *testing.T) {
	assert.Equal(t, int64(42), parseBlockKey([]byte("block:00000000000042")))
	assert.Equal(t, int64(0), parseBlockKey([]byte("block:00000000000000")))
	assert.Equal(t, int64(-1), parseBlockKey([]byte("invalid")))
	assert.Equal(t, int64(-1), parseBlockKey([]byte("short")))
	assert.Equal(t, int64(-1), parseBlockKey([]byte("")))
}

func TestGetCompleteBlock_CacheHit(t *testing.T) {
	c := newTestCache(t)

	// Store a block via fetch
	fetchCount := 0
	data, err := c.GetCompleteBlock(100, func() ([]byte, error) {
		fetchCount++
		return []byte(`{"number":"0x64"}`), nil
	})
	require.NoError(t, err)
	assert.Equal(t, `{"number":"0x64"}`, string(data))
	assert.Equal(t, 1, fetchCount)

	// Second call should be cache hit (fetch not called)
	data2, err := c.GetCompleteBlock(100, func() ([]byte, error) {
		fetchCount++
		return nil, fmt.Errorf("should not be called")
	})
	require.NoError(t, err)
	assert.Equal(t, `{"number":"0x64"}`, string(data2))
	assert.Equal(t, 1, fetchCount) // Still 1 - cache hit
}

func TestGetCompleteBlock_CacheMiss(t *testing.T) {
	c := newTestCache(t)

	data, err := c.GetCompleteBlock(200, func() ([]byte, error) {
		return []byte("block200"), nil
	})
	require.NoError(t, err)
	assert.Equal(t, "block200", string(data))
}

func TestGetCompleteBlock_FetchError(t *testing.T) {
	c := newTestCache(t)

	_, err := c.GetCompleteBlock(300, func() ([]byte, error) {
		return nil, fmt.Errorf("RPC error")
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "RPC error")
}

func TestGetBlockRange(t *testing.T) {
	c := newTestCache(t)

	// Store blocks 10-14
	for i := int64(10); i <= 14; i++ {
		data := []byte(fmt.Sprintf("block%d", i))
		_, err := c.GetCompleteBlock(i, func() ([]byte, error) {
			return data, nil
		})
		require.NoError(t, err)
	}

	// Get range 10-14
	result, err := c.GetBlockRange(10, 14)
	require.NoError(t, err)
	assert.Len(t, result, 5)
	for i := int64(10); i <= 14; i++ {
		assert.Equal(t, fmt.Sprintf("block%d", i), string(result[i]))
	}
}

func TestGetBlockRange_Partial(t *testing.T) {
	c := newTestCache(t)

	// Only store blocks 10, 12, 14 (sparse)
	for _, i := range []int64{10, 12, 14} {
		data := []byte(fmt.Sprintf("block%d", i))
		_, err := c.GetCompleteBlock(i, func() ([]byte, error) {
			return data, nil
		})
		require.NoError(t, err)
	}

	// Get range 10-14 (blocks 11, 13 missing)
	result, err := c.GetBlockRange(10, 14)
	require.NoError(t, err)
	assert.Len(t, result, 3) // Only 3 blocks found
	assert.Equal(t, "block10", string(result[10]))
	assert.Equal(t, "block12", string(result[12]))
	assert.Equal(t, "block14", string(result[14]))
	assert.Nil(t, result[11])
	assert.Nil(t, result[13])
}

func TestGetBlockRange_InvalidRange(t *testing.T) {
	c := newTestCache(t)

	_, err := c.GetBlockRange(20, 10)
	assert.Error(t, err)
}

func TestGetBlockRange_Empty(t *testing.T) {
	c := newTestCache(t)

	result, err := c.GetBlockRange(1000, 1010)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestCheckpoint(t *testing.T) {
	c := newTestCache(t)

	// No checkpoint initially
	cp, err := c.GetCheckpoint()
	require.NoError(t, err)
	assert.Equal(t, int64(0), cp)

	// Set checkpoint
	err = c.SetCheckpoint(12345)
	require.NoError(t, err)

	// Get checkpoint
	cp, err = c.GetCheckpoint()
	require.NoError(t, err)
	assert.Equal(t, int64(12345), cp)

	// Update checkpoint
	err = c.SetCheckpoint(99999)
	require.NoError(t, err)

	cp, err = c.GetCheckpoint()
	require.NoError(t, err)
	assert.Equal(t, int64(99999), cp)
}

func TestConcurrentAccess(t *testing.T) {
	c := newTestCache(t)

	var wg sync.WaitGroup
	numGoroutines := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(blockNum int64) {
			defer wg.Done()
			data := []byte(fmt.Sprintf("block%d", blockNum))
			_, err := c.GetCompleteBlock(blockNum, func() ([]byte, error) {
				return data, nil
			})
			assert.NoError(t, err)
		}(int64(i))
	}

	wg.Wait()

	// Verify all blocks are stored
	result, err := c.GetBlockRange(0, int64(numGoroutines-1))
	require.NoError(t, err)
	assert.Len(t, result, numGoroutines)
}
