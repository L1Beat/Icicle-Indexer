package lending

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// namedAdapter implements only Protocol(); retryUntilUp calls nothing else.
type namedAdapter struct {
	Adapter
	name Protocol
}

func (a namedAdapter) Protocol() Protocol { return a.name }

// A protocol that fails while the node is still starting is retried until it
// comes up, instead of being skipped for the life of the process.
func TestRetryUntilUp_RecoversAfterNodeComesBack(t *testing.T) {
	v3, benqi := namedAdapter{name: "aave-v3"}, namedAdapter{name: "benqi"}
	calls := map[Protocol]int{}
	up := func(a Adapter) error {
		calls[a.Protocol()]++
		if a.Protocol() == "aave-v3" && calls[a.Protocol()] < 3 {
			return errors.New("dial tcp 127.0.0.1:9650: connection refused")
		}
		return nil
	}
	if err := retryUntilUp(context.Background(), []Adapter{v3, benqi}, up, time.Second, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if calls["aave-v3"] != 3 || calls["benqi"] != 1 {
		t.Fatalf("calls %v: want aave-v3 retried until up, benqi brought up once", calls)
	}
}

// A protocol still down at the timeout fails startup, so systemd restarts the
// service rather than it running without that protocol.
func TestRetryUntilUp_FailsAfterTimeout(t *testing.T) {
	up := func(a Adapter) error {
		if a.Protocol() == "benqi" {
			return errors.New("upstream status 503")
		}
		return nil
	}
	err := retryUntilUp(context.Background(), []Adapter{namedAdapter{name: "aave-v3"}, namedAdapter{name: "benqi"}}, up, 20*time.Millisecond, 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "benqi") || strings.Contains(err.Error(), "aave-v3") {
		t.Fatalf("want an error naming only benqi, got %v", err)
	}
}

func TestRetryUntilUp_StopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := retryUntilUp(ctx, []Adapter{namedAdapter{name: "benqi"}}, func(Adapter) error { return errors.New("down") }, time.Hour, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
