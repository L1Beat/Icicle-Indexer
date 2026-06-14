package chwrapper

import (
	"context"
	"testing"
	"time"
)

// TestWriteContext verifies the write deadline is applied (default and override)
// and that it derives from the parent so cancellation still propagates. The
// deadline is the mechanism that lets a batch INSERT into a severed connection
// fail fast instead of parking a pooled connection forever (L1B-51).
func TestWriteContext(t *testing.T) {
	t.Run("default deadline", func(t *testing.T) {
		ctx, cancel := WriteContext(context.Background())
		defer cancel()

		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected a deadline on the derived context, got none")
		}
		got := time.Until(deadline)
		// Allow a little slack for scheduling between WithTimeout and now.
		if got < DefaultWriteTimeout-time.Second || got > DefaultWriteTimeout {
			t.Fatalf("default deadline = %v, want ~%v", got, DefaultWriteTimeout)
		}
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv("CH_WRITE_TIMEOUT_SEC", "7")
		ctx, cancel := WriteContext(context.Background())
		defer cancel()

		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected a deadline on the derived context, got none")
		}
		if got := time.Until(deadline); got < 6*time.Second || got > 7*time.Second {
			t.Fatalf("override deadline = %v, want ~7s", got)
		}
	})

	t.Run("parent cancellation propagates", func(t *testing.T) {
		parent, cancelParent := context.WithCancel(context.Background())
		ctx, cancel := WriteContext(parent)
		defer cancel()

		cancelParent()
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("derived context did not cancel when parent was cancelled")
		}
	})
}
