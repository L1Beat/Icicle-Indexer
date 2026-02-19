package evmindexer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWatermarkKey(t *testing.T) {
	tests := []struct {
		name        string
		indexerName string
		granularity string
		expected    string
	}{
		{
			name:        "without granularity",
			indexerName: "token_balances",
			granularity: "",
			expected:    "token_balances",
		},
		{
			name:        "with granularity",
			indexerName: "chain_metrics",
			granularity: "hour",
			expected:    "chain_metrics:hour",
		},
		{
			name:        "with day granularity",
			indexerName: "fee_stats",
			granularity: "day",
			expected:    "fee_stats:day",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := watermarkKey(tt.indexerName, tt.granularity)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWatermarkStruct(t *testing.T) {
	t.Run("zero value", func(t *testing.T) {
		wm := Watermark{}
		assert.True(t, wm.LastPeriod.IsZero())
		assert.Equal(t, uint64(0), wm.LastBlockNum)
	})

	t.Run("with values", func(t *testing.T) {
		now := time.Now().UTC()
		wm := Watermark{
			LastPeriod:   now,
			LastBlockNum: 12345678,
		}
		assert.Equal(t, now, wm.LastPeriod)
		assert.Equal(t, uint64(12345678), wm.LastBlockNum)
	})
}
