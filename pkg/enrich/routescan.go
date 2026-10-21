package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// RoutescanClient wraps the Etherscan-compatible getsourcecode endpoint.
// Calls are throttled to one per MinInterval and retried on transient errors
// (the endpoint was observed to reset connections and time out sporadically).
type RoutescanClient struct {
	BaseURL     string // e.g. https://api.routescan.io/v2/network/mainnet/evm/43114/etherscan/api
	APIKey      string // optional, ICICLE_ROUTESCAN_API_KEY
	HTTPClient  *http.Client
	MinInterval time.Duration

	mu   sync.Mutex
	last time.Time
}

// SourceInfo is the subset of getsourcecode we keep.
type SourceInfo struct {
	Verified       bool
	ContractName   string
	Proxy          bool
	Implementation string // 0x-prefixed lowercase, "" if none
}

func NewRoutescanClient(chainID uint32, apiKey string) *RoutescanClient {
	return &RoutescanClient{
		BaseURL:     fmt.Sprintf("https://api.routescan.io/v2/network/mainnet/evm/%d/etherscan/api", chainID),
		APIKey:      apiKey,
		HTTPClient:  &http.Client{Timeout: 25 * time.Second},
		MinInterval: 600 * time.Millisecond,
	}
}

func (c *RoutescanClient) throttle(ctx context.Context) error {
	c.mu.Lock()
	wait := time.Until(c.last.Add(c.MinInterval))
	c.last = time.Now().Add(maxDuration(wait, 0))
	c.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// GetSourceCode returns verification info for addr (any case). An unverified
// contract is not an error: Verified is false.
func (c *RoutescanClient) GetSourceCode(ctx context.Context, addr string) (*SourceInfo, error) {
	q := url.Values{}
	q.Set("module", "contract")
	q.Set("action", "getsourcecode")
	q.Set("address", addr)
	if c.APIKey != "" {
		q.Set("apikey", c.APIKey)
	}
	endpoint := c.BaseURL + "?" + q.Encode()

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := c.throttle(ctx); err != nil {
			return nil, err
		}
		info, retryAfter, err := c.fetchOnce(ctx, endpoint)
		if err == nil {
			return info, nil
		}
		lastErr = err
		backoff := time.Duration(attempt+1) * 2 * time.Second
		if retryAfter > backoff {
			backoff = retryAfter
		}
		if backoff > time.Minute {
			backoff = time.Minute
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
	return nil, lastErr
}

func (c *RoutescanClient) fetchOnce(ctx context.Context, endpoint string) (*SourceInfo, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "icicle-indexer (+https://l1beat.io)")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("routescan: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("routescan: read: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		ra, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return nil, time.Duration(ra) * time.Second, fmt.Errorf("routescan: HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("routescan: HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var parsed struct {
		Status  string          `json:"status"`
		Message string          `json:"message"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, 0, fmt.Errorf("routescan: decode: %w", err)
	}
	var results []struct {
		ABI            string `json:"ABI"`
		ContractName   string `json:"ContractName"`
		Proxy          string `json:"Proxy"`
		Implementation string `json:"Implementation"`
	}
	if err := json.Unmarshal(parsed.Result, &results); err != nil {
		// Etherscan-style error payloads put a string in result.
		return nil, 0, fmt.Errorf("routescan: %s: %s", parsed.Message, truncate(string(parsed.Result), 200))
	}
	if len(results) == 0 {
		return &SourceInfo{}, 0, nil
	}
	r := results[0]
	info := &SourceInfo{
		Verified:     r.ContractName != "" && r.ABI != "Contract source code not verified",
		ContractName: r.ContractName,
		Proxy:        r.Proxy == "1",
	}
	if impl, ok := NormalizeAddress(r.Implementation); ok {
		info.Implementation = impl
	}
	return info, 0, nil
}
