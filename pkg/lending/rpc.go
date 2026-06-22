package lending

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Client is a small JSON-RPC client for the read path. It reads from our own
// archive node first and only falls back to a configured public RPC, logging the
// first time it does (non-functional requirement). It backs off and retries on
// transient failures and honors Retry-After on 429.
type Client struct {
	archiveURL  string
	fallbackURL string // optional
	http        *http.Client
	maxRetries  int
	warnedOnce  atomic.Bool
}

// NewClient builds a Client. fallbackURL may be empty.
func NewClient(archiveURL, fallbackURL string) *Client {
	return &Client{
		archiveURL:  archiveURL,
		fallbackURL: fallbackURL,
		http:        &http.Client{Timeout: 20 * time.Second},
		maxRetries:  4,
	}
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// EthCall performs eth_call at the given block tag ("latest" when empty).
func (c *Client) EthCall(ctx context.Context, to, data, block string) (string, error) {
	if block == "" {
		block = "latest"
	}
	var result string
	err := c.call(ctx, "eth_call", []interface{}{
		map[string]string{"to": to, "data": ensure0x(data)}, block,
	}, &result)
	return result, err
}

// GasPrice returns the current network gas price in wei.
func (c *Client) GasPrice(ctx context.Context) (*big.Int, error) {
	var h string
	if err := c.call(ctx, "eth_gasPrice", []interface{}{}, &h); err != nil {
		return nil, err
	}
	s := strings.TrimPrefix(strings.TrimPrefix(h, "0x"), "0X")
	if s == "" {
		return big.NewInt(0), nil
	}
	n, ok := new(big.Int).SetString(s, 16)
	if !ok {
		return nil, fmt.Errorf("invalid gas price %q", h)
	}
	return n, nil
}

// BlockNumber returns the current chain head.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	var hexNum string
	if err := c.call(ctx, "eth_blockNumber", []interface{}{}, &hexNum); err != nil {
		return 0, err
	}
	n := new(bigFromHex)
	return n.parse(hexNum)
}

// call runs a JSON-RPC method with retries against the archive node, falling back
// to the public RPC if one is configured and archive attempts are exhausted.
func (c *Client) call(ctx context.Context, method string, params []interface{}, out interface{}) error {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params, ID: 1})
	if err != nil {
		return err
	}

	lastErr := c.attempt(ctx, c.archiveURL, body, out)
	if lastErr == nil {
		return nil
	}
	if c.fallbackURL != "" {
		if c.warnedOnce.CompareAndSwap(false, true) {
			slog.Warn("lending: archive RPC failed, using configured fallback RPC", "method", method, "error", lastErr)
		}
		if err := c.attempt(ctx, c.fallbackURL, body, out); err == nil {
			return nil
		}
	}
	return lastErr
}

// attempt sends one logical request to a single endpoint with backoff retries.
func (c *Client) attempt(ctx context.Context, url string, body []byte, out interface{}) error {
	var lastErr error
	backoff := 250 * time.Millisecond
	for i := 0; i <= c.maxRetries; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, retryAfter, err := c.do(ctx, url, body)
		if err == nil {
			var resp rpcResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				lastErr = fmt.Errorf("decode rpc response: %w", err)
			} else if resp.Error != nil {
				// A contract revert is a definitive answer, not a transient error.
				return fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
			} else {
				return json.Unmarshal(resp.Result, out)
			}
		} else {
			lastErr = err
		}

		wait := backoff
		if retryAfter > 0 {
			wait = retryAfter
		}
		if i < c.maxRetries {
			if !sleepCtx(ctx, wait) {
				return ctx.Err()
			}
			backoff *= 2
		}
	}
	return lastErr
}

// do performs a single HTTP POST and reports a Retry-After hint on 429.
func (c *Client) do(ctx context.Context, url string, body []byte) ([]byte, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, parseRetryAfter(resp.Header.Get("Retry-After")), fmt.Errorf("rate limited (429)")
	}
	if resp.StatusCode >= 500 {
		return nil, 0, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	return raw, 0, nil
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return time.Duration(secs) * time.Second
	}
	return 0
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func ensure0x(s string) string {
	if len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		return s
	}
	return "0x" + s
}

// bigFromHex parses a 0x-prefixed quantity.
type bigFromHex struct{}

func (bigFromHex) parse(s string) (uint64, error) {
	if len(s) > 2 && (s[1] == 'x' || s[1] == 'X') {
		s = s[2:]
	}
	if s == "" {
		return 0, nil
	}
	return strconv.ParseUint(s, 16, 64)
}
