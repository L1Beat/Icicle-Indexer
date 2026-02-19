package evmindexer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToStartOfPeriod_Hour(t *testing.T) {
	input := time.Date(2025, 6, 15, 14, 37, 22, 0, time.UTC)
	expected := time.Date(2025, 6, 15, 14, 0, 0, 0, time.UTC)
	assert.Equal(t, expected, toStartOfPeriod(input, "hour"))
}

func TestToStartOfPeriod_Day(t *testing.T) {
	input := time.Date(2025, 6, 15, 14, 37, 22, 0, time.UTC)
	expected := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, expected, toStartOfPeriod(input, "day"))
}

func TestToStartOfPeriod_Week(t *testing.T) {
	// June 15, 2025 is a Sunday
	input := time.Date(2025, 6, 15, 14, 37, 22, 0, time.UTC)
	expected := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, expected, toStartOfPeriod(input, "week"))

	// June 18, 2025 is a Wednesday - should go back to Sunday June 15
	input2 := time.Date(2025, 6, 18, 10, 0, 0, 0, time.UTC)
	assert.Equal(t, expected, toStartOfPeriod(input2, "week"))
}

func TestToStartOfPeriod_Month(t *testing.T) {
	input := time.Date(2025, 6, 15, 14, 37, 22, 0, time.UTC)
	expected := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, expected, toStartOfPeriod(input, "month"))
}

func TestToStartOfPeriod_NonUTCInput(t *testing.T) {
	// Non-UTC input should be converted to UTC
	loc := time.FixedZone("EST", -5*3600)
	input := time.Date(2025, 6, 15, 14, 37, 22, 0, loc)
	result := toStartOfPeriod(input, "hour")
	assert.Equal(t, time.UTC, result.Location())
}

func TestToStartOfPeriod_UnknownGranularity(t *testing.T) {
	input := time.Date(2025, 6, 15, 14, 0, 0, 0, time.UTC)
	assert.Panics(t, func() {
		toStartOfPeriod(input, "minute")
	})
}

func TestGetPeriodDuration(t *testing.T) {
	assert.Equal(t, time.Hour, getPeriodDuration("hour"))
	assert.Equal(t, 24*time.Hour, getPeriodDuration("day"))
	assert.Equal(t, 7*24*time.Hour, getPeriodDuration("week"))
	assert.Equal(t, 30*24*time.Hour, getPeriodDuration("month"))
}

func TestGetPeriodDuration_UnknownGranularity(t *testing.T) {
	assert.Panics(t, func() {
		getPeriodDuration("year")
	})
}

func TestNextPeriod(t *testing.T) {
	tests := []struct {
		name        string
		input       time.Time
		granularity string
		expected    time.Time
	}{
		{
			name:        "hour",
			input:       time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC),
			granularity: "hour",
			expected:    time.Date(2025, 6, 15, 15, 0, 0, 0, time.UTC),
		},
		{
			name:        "day",
			input:       time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC),
			granularity: "day",
			expected:    time.Date(2025, 6, 16, 0, 0, 0, 0, time.UTC),
		},
		{
			name:        "week",
			input:       time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC), // Sunday
			granularity: "week",
			expected:    time.Date(2025, 6, 22, 0, 0, 0, 0, time.UTC),   // Next Sunday
		},
		{
			name:        "month boundary",
			input:       time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC),
			granularity: "month",
			expected:    time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:        "month december to january",
			input:       time.Date(2025, 12, 15, 0, 0, 0, 0, time.UTC),
			granularity: "month",
			expected:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := nextPeriod(tt.input, tt.granularity)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsPeriodComplete(t *testing.T) {
	periodStart := time.Date(2025, 6, 15, 14, 0, 0, 0, time.UTC)

	// Block time before period end - not complete
	blockTimeBefore := time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC)
	assert.False(t, isPeriodComplete(periodStart, blockTimeBefore, "hour"))

	// Block time at period end - complete
	blockTimeExact := time.Date(2025, 6, 15, 15, 0, 0, 0, time.UTC)
	assert.True(t, isPeriodComplete(periodStart, blockTimeExact, "hour"))

	// Block time after period end - complete
	blockTimeAfter := time.Date(2025, 6, 15, 16, 0, 0, 0, time.UTC)
	assert.True(t, isPeriodComplete(periodStart, blockTimeAfter, "hour"))
}

func TestGetPeriodsToProcess(t *testing.T) {
	t.Run("no periods when zero lastProcessed", func(t *testing.T) {
		periods := getPeriodsToProcess(time.Time{}, time.Now(), "hour")
		assert.Empty(t, periods)
	})

	t.Run("returns complete hourly periods", func(t *testing.T) {
		lastProcessed := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
		latestBlock := time.Date(2025, 6, 15, 13, 30, 0, 0, time.UTC)

		periods := getPeriodsToProcess(lastProcessed, latestBlock, "hour")
		// next period after 10:00 is 11:00.
		// isPeriodComplete(11:00, 13:30, "hour") -> nextPeriod(11:00) = 12:00, 13:30 > 12:00 -> true
		// isPeriodComplete(12:00, 13:30, "hour") -> nextPeriod(12:00) = 13:00, 13:30 > 13:00 -> true
		// isPeriodComplete(13:00, 13:30, "hour") -> nextPeriod(13:00) = 14:00, 13:30 < 14:00 -> false
		// So 2 complete periods: 11:00 and 12:00
		require.Len(t, periods, 2)
		assert.Equal(t, time.Date(2025, 6, 15, 11, 0, 0, 0, time.UTC), periods[0])
		assert.Equal(t, time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC), periods[1])
	})

	t.Run("returns complete daily periods", func(t *testing.T) {
		lastProcessed := time.Date(2025, 6, 13, 0, 0, 0, 0, time.UTC)
		latestBlock := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

		periods := getPeriodsToProcess(lastProcessed, latestBlock, "day")
		// Next after June 13 is June 14
		// isPeriodComplete(June 14, June 15 12:00) -> nextPeriod(June 14) = June 15, 12:00 > June 15 -> true
		// isPeriodComplete(June 15, June 15 12:00) -> nextPeriod(June 15) = June 16, 12:00 < June 16 -> false
		require.Len(t, periods, 1)
		assert.Equal(t, time.Date(2025, 6, 14, 0, 0, 0, 0, time.UTC), periods[0])
	})

	t.Run("no periods when not enough time passed", func(t *testing.T) {
		lastProcessed := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
		latestBlock := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)

		periods := getPeriodsToProcess(lastProcessed, latestBlock, "hour")
		assert.Empty(t, periods)
	})
}
