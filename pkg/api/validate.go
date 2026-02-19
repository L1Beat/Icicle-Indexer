package api

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// validateAddress extracts and validates an Ethereum address from the request path.
// Returns the lowercase hex string without 0x prefix (ready for unhex() in SQL).
func validateAddress(r *http.Request) (string, error) {
	raw := r.PathValue("address")
	raw = strings.ToLower(raw)
	raw = strings.TrimPrefix(raw, "0x")

	if len(raw) != 40 {
		return "", fmt.Errorf("address must be 20 bytes (40 hex chars), got %d", len(raw))
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return "", fmt.Errorf("address contains invalid hex characters")
	}
	return raw, nil
}

// validateTxHash extracts and validates a transaction hash from the request path.
// Returns the lowercase hex string without 0x prefix (ready for unhex() in SQL).
func validateTxHash(r *http.Request) (string, error) {
	raw := r.PathValue("hash")
	raw = strings.ToLower(raw)
	raw = strings.TrimPrefix(raw, "0x")

	if len(raw) != 64 {
		return "", fmt.Errorf("tx hash must be 32 bytes (64 hex chars), got %d", len(raw))
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return "", fmt.Errorf("tx hash contains invalid hex characters")
	}
	return raw, nil
}

// validateGranularity validates a time granularity parameter.
func validateGranularity(r *http.Request) (string, error) {
	g := r.URL.Query().Get("granularity")
	if g == "" {
		return "day", nil // default
	}
	switch g {
	case "hour", "day", "week", "month":
		return g, nil
	default:
		return "", fmt.Errorf("invalid granularity %q, must be one of: hour, day, week, month", g)
	}
}
