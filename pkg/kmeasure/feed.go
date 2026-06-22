package kmeasure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// rawLeg mirrors one lending_position_assets leg in the feed JSON.
type rawLeg struct {
	Asset     string `json:"asset"`
	Symbol    string `json:"symbol"`
	Amount    string `json:"amount"`
	BaseValue string `json:"base_value"`
}

// rawPosition mirrors one position in the feed JSON. Money fields are 1e18-scaled
// USD decimal strings, amounts are native-unit strings.
type rawPosition struct {
	Account        string   `json:"account"`
	Protocol       string   `json:"protocol"`
	HealthFactor   string   `json:"health_factor"`
	Liquidatable   bool     `json:"liquidatable"`
	CollateralBase string   `json:"collateral_base"`
	DebtBase       string   `json:"debt_base"`
	ShortfallBase  string   `json:"shortfall_base"`
	Tier           string   `json:"tier"`
	Collateral     []rawLeg `json:"collateral"`
	Debt           []rawLeg `json:"debt"`
	BlockNumber    uint64   `json:"block_number"`
}

type feedEnvelope struct {
	Data []rawPosition `json:"data"`
	Meta *struct {
		HasMore bool `json:"has_more"`
	} `json:"meta"`
}

type statsEnvelope struct {
	Data []struct {
		Protocol      string `json:"protocol"`
		OpenPositions uint64 `json:"open_positions"`
		Liquidatable  uint64 `json:"liquidatable"`
	} `json:"data"`
}

// FeedClient reads the lending feed over the public REST API, the same surface an
// external searcher would use.
type FeedClient struct {
	baseURL string
	http    *http.Client
}

// NewFeedClient builds a feed client. baseURL is the lending base, for example
// https://api.l1beat.io/api/v1/data/evm/43114/lending
func NewFeedClient(baseURL string) *FeedClient {
	return &FeedClient{baseURL: baseURL, http: &http.Client{Timeout: 30 * time.Second}}
}

// Stats returns the headline counts, for context and logging only.
func (f *FeedClient) Stats(ctx context.Context) (statsEnvelope, error) {
	var s statsEnvelope
	err := f.getJSON(ctx, f.baseURL+"/stats", &s)
	return s, err
}

// FetchLiquidatable pages the positions feed and returns the full liquidatable
// set. The feed is ordered liquidatable first, so paging stops at the first
// non-liquidatable row. minDebtBase, when non-empty, applies the feed's own dust
// pre-cut; leave it empty so the pre-filter classifies dust itself.
func (f *FeedClient) FetchLiquidatable(ctx context.Context, minDebtBase string) ([]rawPosition, error) {
	const limit = 100
	const maxOffset = 10000

	var out []rawPosition
	for offset := 0; offset <= maxOffset; offset += limit {
		q := url.Values{}
		q.Set("limit", fmt.Sprintf("%d", limit))
		q.Set("offset", fmt.Sprintf("%d", offset))
		if minDebtBase != "" {
			q.Set("min_debt_base", minDebtBase)
		}

		var env feedEnvelope
		if err := f.getJSON(ctx, f.baseURL+"/positions?"+q.Encode(), &env); err != nil {
			return nil, err
		}
		if len(env.Data) == 0 {
			break
		}

		stop := false
		for _, p := range env.Data {
			if !p.Liquidatable {
				stop = true // ordered liquidatable first, so the rest are all healthy
				break
			}
			out = append(out, p)
		}
		if stop {
			break
		}
		if env.Meta == nil || !env.Meta.HasMore {
			break
		}
	}
	return out, nil
}

func (f *FeedClient) getJSON(ctx context.Context, fullURL string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return err
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("feed %s: status %d", fullURL, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
