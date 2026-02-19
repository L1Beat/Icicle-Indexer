package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// State represents the circuit breaker state.
type State int

const (
	Closed   State = iota // Normal operation
	Open                  // Failing fast
	HalfOpen              // Trying one request
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ErrCircuitOpen is returned when the circuit breaker is open.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// Options configures a circuit breaker.
type Options struct {
	FailureThreshold int           // Consecutive failures before opening (default: 5)
	CooldownPeriod   time.Duration // Time to wait before half-open (default: 30s)
}

// CircuitBreaker implements a simple circuit breaker pattern.
type CircuitBreaker struct {
	mu               sync.Mutex
	state            State
	consecutiveFails int
	failureThreshold int
	cooldownPeriod   time.Duration
	openedAt         time.Time
}

// New creates a circuit breaker with the given options.
func New(opts Options) *CircuitBreaker {
	if opts.FailureThreshold == 0 {
		opts.FailureThreshold = 5
	}
	if opts.CooldownPeriod == 0 {
		opts.CooldownPeriod = 30 * time.Second
	}
	return &CircuitBreaker{
		state:            Closed,
		failureThreshold: opts.FailureThreshold,
		cooldownPeriod:   opts.CooldownPeriod,
	}
}

// Allow checks if a request is allowed. Returns an error if the circuit is open.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case Closed:
		return nil
	case Open:
		if time.Since(cb.openedAt) >= cb.cooldownPeriod {
			cb.state = HalfOpen
			return nil
		}
		return ErrCircuitOpen
	case HalfOpen:
		// Only one request at a time in half-open; block others
		return ErrCircuitOpen
	}
	return nil
}

// RecordSuccess records a successful request.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFails = 0
	cb.state = Closed
}

// RecordFailure records a failed request and opens the circuit if threshold is reached.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFails++
	if cb.consecutiveFails >= cb.failureThreshold {
		cb.state = Open
		cb.openedAt = time.Now()
	}
}

// State returns the current effective circuit state without side effects.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state == Open && time.Since(cb.openedAt) >= cb.cooldownPeriod {
		return HalfOpen
	}
	return cb.state
}

// ConsecutiveFailures returns the current consecutive failure count.
func (cb *CircuitBreaker) ConsecutiveFailures() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.consecutiveFails
}
