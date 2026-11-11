package lending

import (
	"strings"
	"testing"
	"time"
)

func TestHex40(t *testing.T) {
	if h, ok := hex40("0xAbCdEf0123456789abcdef0123456789ABCDEF01"); !ok || h != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("valid address rejected: %q %v", h, ok)
	}
	for _, bad := range []string{"", "0x12", "0x" + strings.Repeat("g", 40), "') OR 1=1 --" + strings.Repeat("a", 28)} {
		if _, ok := hex40(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
}

// The stamp must keep milliseconds; a whole-second stamp would put carried legs
// before pos_ts and hide them again.
func TestCarryLegsSQLKeepsMilliseconds(t *testing.T) {
	q := carryLegsSQL("(unhex('aa'), unhex('bb'))", "unhex('aa')", time.Date(2026, 11, 11, 21, 8, 24, 123_000_000, time.UTC))
	if !strings.Contains(q, "toDateTime64('2026-11-11 21:08:24.123', 3, 'UTC')") {
		t.Fatalf("stamp literal missing or truncated:\n%s", q)
	}
	if !strings.Contains(q, "l.leg_ts >= p.pos_ts") || !strings.Contains(q, "l.amt > 0") {
		t.Fatal("carry must apply the same liveness rule as the API readers")
	}
}
