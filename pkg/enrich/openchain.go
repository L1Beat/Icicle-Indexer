package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OpenChainClient resolves selectors through the OpenChain lookup API. The
// bulk export endpoint documented in the OpenChain repo returns 404 as of
// 2026-10-21, so resolution is demand-driven: selectors actually seen on our
// chains are looked up in batches and cached in ClickHouse. Only the indexer
// (never the API request path) calls this.
type OpenChainClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// OpenChainBatchSize is the number of selectors per lookup call. The API was
// probed with 200 and answered in full.
const OpenChainBatchSize = 200

func NewOpenChainClient() *OpenChainClient {
	return &OpenChainClient{
		BaseURL:    "https://api.openchain.xyz/signature-database/v1/lookup",
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type openChainResponse struct {
	OK     bool `json:"ok"`
	Result struct {
		Function map[string][]struct {
			Name                string `json:"name"`
			Filtered            bool   `json:"filtered"`
			HasVerifiedContract bool   `json:"hasVerifiedContract"`
		} `json:"function"`
	} `json:"result"`
}

// Lookup resolves up to OpenChainBatchSize selectors. The result maps every
// requested selector to its candidates; a selector with no known signature maps
// to an empty slice so callers can negative-cache it.
func (c *OpenChainClient) Lookup(ctx context.Context, selectors [][4]byte) (map[[4]byte][]SignatureRow, error) {
	if len(selectors) == 0 {
		return map[[4]byte][]SignatureRow{}, nil
	}
	if len(selectors) > OpenChainBatchSize {
		return nil, fmt.Errorf("openchain: batch of %d exceeds %d", len(selectors), OpenChainBatchSize)
	}
	hexes := make([]string, len(selectors))
	for i, s := range selectors {
		hexes[i] = SelectorHex(s)
	}
	q := url.Values{}
	q.Set("function", strings.Join(hexes, ","))
	q.Set("filter", "true")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "icicle-indexer (+https://l1beat.io)")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openchain: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("openchain: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openchain: HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var parsed openChainResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("openchain: decode: %w", err)
	}
	if !parsed.OK {
		return nil, fmt.Errorf("openchain: ok=false")
	}

	out := make(map[[4]byte][]SignatureRow, len(selectors))
	for _, sel := range selectors {
		out[sel] = []SignatureRow{}
		for _, hit := range parsed.Result.Function[SelectorHex(sel)] {
			if hit.Filtered || hit.Name == "" {
				continue
			}
			// Guard against a mislabelled entry: the signature must hash to the selector.
			if SelectorOf(hit.Name) != sel {
				continue
			}
			prio := PriorityOpenChain
			if hit.HasVerifiedContract {
				prio = PriorityOpenChainVerified
			}
			out[sel] = append(out[sel], SignatureRow{
				Selector:  sel,
				Signature: hit.Name,
				Name:      FunctionName(hit.Name),
				Source:    "openchain",
				Priority:  prio,
			})
		}
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
