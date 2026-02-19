package circuitbreaker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCircuitBreaker_StartsClosedAndAllows(t *testing.T) {
	cb := New(Options{FailureThreshold: 3, CooldownPeriod: 100 * time.Millisecond})
	assert.Equal(t, Closed, cb.State())
	assert.NoError(t, cb.Allow())
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := New(Options{FailureThreshold: 3, CooldownPeriod: 100 * time.Millisecond})

	cb.RecordFailure()
	cb.RecordFailure()
	assert.Equal(t, Closed, cb.State())
	assert.NoError(t, cb.Allow())

	cb.RecordFailure() // 3rd failure = threshold
	assert.Equal(t, Open, cb.State())
	assert.ErrorIs(t, cb.Allow(), ErrCircuitOpen)
}

func TestCircuitBreaker_SuccessResetsFailures(t *testing.T) {
	cb := New(Options{FailureThreshold: 3, CooldownPeriod: 100 * time.Millisecond})

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess() // Reset
	assert.Equal(t, 0, cb.ConsecutiveFailures())

	cb.RecordFailure()
	cb.RecordFailure()
	assert.Equal(t, Closed, cb.State()) // Still closed, only 2 after reset
}

func TestCircuitBreaker_TransitionsToHalfOpen(t *testing.T) {
	cb := New(Options{FailureThreshold: 2, CooldownPeriod: 50 * time.Millisecond})

	cb.RecordFailure()
	cb.RecordFailure()
	assert.Equal(t, Open, cb.State())

	// Wait for cooldown
	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, HalfOpen, cb.State())

	// First request allowed
	require.NoError(t, cb.Allow())

	// Second request in half-open is blocked
	assert.ErrorIs(t, cb.Allow(), ErrCircuitOpen)
}

func TestCircuitBreaker_HalfOpenSuccessCloses(t *testing.T) {
	cb := New(Options{FailureThreshold: 2, CooldownPeriod: 50 * time.Millisecond})

	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)

	require.NoError(t, cb.Allow()) // half-open allows one
	cb.RecordSuccess()
	assert.Equal(t, Closed, cb.State())
	assert.NoError(t, cb.Allow()) // closed, allows all
}

func TestCircuitBreaker_HalfOpenFailureReopens(t *testing.T) {
	cb := New(Options{FailureThreshold: 2, CooldownPeriod: 50 * time.Millisecond})

	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)

	require.NoError(t, cb.Allow()) // half-open allows one
	cb.RecordFailure()
	assert.Equal(t, Open, cb.State())
	assert.ErrorIs(t, cb.Allow(), ErrCircuitOpen)
}

func TestCircuitBreaker_DefaultOptions(t *testing.T) {
	cb := New(Options{})
	assert.Equal(t, 5, cb.failureThreshold)
	assert.Equal(t, 30*time.Second, cb.cooldownPeriod)
}

func TestState_String(t *testing.T) {
	assert.Equal(t, "closed", Closed.String())
	assert.Equal(t, "open", Open.String())
	assert.Equal(t, "half-open", HalfOpen.String())
}
