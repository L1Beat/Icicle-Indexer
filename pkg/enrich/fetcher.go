package enrich

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Enricher runs the background work that keeps function_signatures and
// address_labels populated for one chain. It is driven by the indexer loop
// (small incremental steps) and by the enrich-backfill command (one big step).
type Enricher struct {
	ChainID uint32
	Conn    driver.Conn
	RPCURL  string

	OpenChain *OpenChainClient
	Routescan *RoutescanClient
	rpcClient *http.Client

	// UnverifiedRetry is how long a negative-cache label row (EOA or
	// unverified contract) is trusted before the address is re-checked.
	UnverifiedRetry time.Duration
	// UnknownSelectorRetry is how long a negative-cache selector row is trusted.
	UnknownSelectorRetry time.Duration

	knownSelectors map[[4]byte]time.Time
	knownLabels    map[string]time.Time
	lastSigBlock   uint64
	lastLabelBlock uint64
}

func NewEnricher(chainID uint32, conn driver.Conn, rpcURL string) *Enricher {
	return &Enricher{
		ChainID:              chainID,
		Conn:                 conn,
		RPCURL:               rpcURL,
		OpenChain:            NewOpenChainClient(),
		Routescan:            NewRoutescanClient(chainID, os.Getenv("ICICLE_ROUTESCAN_API_KEY")),
		rpcClient:            &http.Client{Timeout: 20 * time.Second},
		UnverifiedRetry:      30 * 24 * time.Hour,
		UnknownSelectorRetry: 7 * 24 * time.Hour,
	}
}

// SeedAll writes the bundled signature seed and the curated labels for this
// chain. Idempotent; run at every startup so repo edits reach the table.
func (e *Enricher) SeedAll(ctx context.Context) error {
	if err := UpsertSignatures(ctx, e.Conn, SeedSignatures()); err != nil {
		return err
	}
	curated, err := CuratedLabels()
	if err != nil {
		return err
	}
	return UpsertLabels(ctx, e.Conn, e.ChainID, curated[e.ChainID])
}

func (e *Enricher) loadKnown(ctx context.Context) error {
	if e.knownSelectors == nil {
		sels, err := KnownSelectors(ctx, e.Conn)
		if err != nil {
			return err
		}
		e.knownSelectors = sels
	}
	if e.knownLabels == nil {
		addrs, err := KnownLabelAddresses(ctx, e.Conn, e.ChainID)
		if err != nil {
			return err
		}
		e.knownLabels = addrs
	}
	return nil
}

// ResolveSignatures looks up every selector used in [fromBlock, toBlock] that
// is not yet known. Returns (selectors looked up, selectors resolved).
func (e *Enricher) ResolveSignatures(ctx context.Context, fromBlock, toBlock uint64, maxSelectors int) (int, int, error) {
	if err := e.loadKnown(ctx); err != nil {
		return 0, 0, err
	}
	seen, err := SelectorsInWindow(ctx, e.Conn, e.ChainID, fromBlock, toBlock, maxSelectors)
	if err != nil {
		return 0, 0, err
	}
	var todo [][4]byte
	for _, s := range seen {
		if _, ok := e.knownSelectors[s]; !ok {
			todo = append(todo, s)
		}
	}
	if len(todo) == 0 {
		return 0, 0, nil
	}
	resolved, err := ResolveSelectors(ctx, e.Conn, e.OpenChain, todo)
	now := time.Now()
	for _, s := range todo {
		e.knownSelectors[s] = now
	}
	return len(todo), resolved, err
}

// RetryUnknownSelectors re-queries negative-cached selectors older than
// UnknownSelectorRetry (the "weekly refresh").
func (e *Enricher) RetryUnknownSelectors(ctx context.Context, limit int) (int, error) {
	stale, err := StaleUnknownSelectors(ctx, e.Conn, e.UnknownSelectorRetry, limit)
	if err != nil || len(stale) == 0 {
		return 0, err
	}
	return ResolveSelectors(ctx, e.Conn, e.OpenChain, stale)
}

// FetchLabels labels up to maxAddresses of the most active unseen contract
// addresses in [fromBlock, toBlock]. Returns (addresses processed, verified).
func (e *Enricher) FetchLabels(ctx context.Context, fromBlock, toBlock uint64, maxAddresses int) (int, int, error) {
	if err := e.loadKnown(ctx); err != nil {
		return 0, 0, err
	}
	// Over-fetch candidates so the skip list still leaves enough work.
	cands, err := ContractCandidates(ctx, e.Conn, e.ChainID, fromBlock, toBlock, maxAddresses*4+100)
	if err != nil {
		return 0, 0, err
	}
	var todo []string
	retryBefore := time.Now().Add(-e.UnverifiedRetry)
	for _, a := range cands {
		if at, ok := e.knownLabels[a]; ok && at.After(retryBefore) {
			continue
		}
		todo = append(todo, a)
		if len(todo) >= maxAddresses {
			break
		}
	}
	return e.LabelAddresses(ctx, todo)
}

// LabelAddresses resolves and stores a label for each address.
func (e *Enricher) LabelAddresses(ctx context.Context, addrs []string) (int, int, error) {
	if err := e.loadKnown(ctx); err != nil {
		return 0, 0, err
	}
	processed, verified := 0, 0
	var pending []Label
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		if err := UpsertLabels(ctx, e.Conn, e.ChainID, pending); err != nil {
			return err
		}
		pending = pending[:0]
		return nil
	}
	for _, addr := range addrs {
		if ctx.Err() != nil {
			return processed, verified, flush()
		}
		label, err := e.labelFor(ctx, addr)
		if err != nil {
			slog.Warn("enrich: label lookup failed", "chain_id", e.ChainID, "address", addr, "error", err)
			continue
		}
		processed++
		if label.Source == SourceVerifiedSource {
			verified++
		}
		e.knownLabels[addr] = time.Now()
		pending = append(pending, label)
		if len(pending) >= 50 {
			if err := flush(); err != nil {
				return processed, verified, err
			}
		}
	}
	return processed, verified, flush()
}

// labelFor builds the label for one address: our node decides contract vs
// EOA, Routescan supplies the verified name, proxies are followed one hop.
func (e *Enricher) labelFor(ctx context.Context, addr string) (Label, error) {
	negative := Label{Address: addr, Source: SourceUnverified}
	if e.RPCURL != "" {
		isContract, err := hasCode(ctx, e.rpcClient, e.RPCURL, addr)
		if err != nil {
			return Label{}, err
		}
		negative.IsContract = isContract
		if !isContract {
			return negative, nil // EOA: nothing to look up
		}
	}
	info, err := e.Routescan.GetSourceCode(ctx, addr)
	if err != nil {
		return Label{}, err
	}
	if !info.Verified {
		return negative, nil
	}
	label := Label{
		Address:        addr,
		Name:           info.ContractName,
		IsContract:     true,
		Verified:       true,
		Implementation: info.Implementation,
		Source:         SourceVerifiedSource,
	}
	if info.Proxy && label.Implementation == "" && e.RPCURL != "" {
		if impl, err := eip1967Implementation(ctx, e.rpcClient, e.RPCURL, addr); err == nil {
			label.Implementation = impl
		}
	}
	if label.Implementation != "" && IsProxyName(label.Name) {
		if implInfo, err := e.Routescan.GetSourceCode(ctx, label.Implementation); err == nil && implInfo.Verified && implInfo.ContractName != "" {
			label.Name = implInfo.ContractName
		}
	}
	label.Kind = KindFromContractName(label.Name)
	return label, nil
}

// --- incremental driver used by the indexer loop ---------------------------

// Step runs one bounded increment: new selectors and new contracts since the
// last step (first step covers the trailing day). It is designed to be called
// every few tens of seconds and to do nothing when nothing changed.
func (e *Enricher) Step(ctx context.Context) {
	latest, err := LatestBlock(ctx, e.Conn, e.ChainID)
	if err != nil || latest == 0 {
		return
	}
	if e.lastSigBlock == 0 {
		start, err := BlockAtOrAfter(ctx, e.Conn, e.ChainID, time.Now().Add(-24*time.Hour))
		if err != nil || start == 0 {
			start = latest
		}
		e.lastSigBlock, e.lastLabelBlock = start-1, start-1
	}
	if latest > e.lastSigBlock {
		looked, resolved, err := e.ResolveSignatures(ctx, e.lastSigBlock+1, latest, 2000)
		if err != nil {
			slog.Warn("enrich: signature resolution failed", "chain_id", e.ChainID, "error", err)
		} else {
			if looked > 0 {
				slog.Info("enrich: resolved selectors", "chain_id", e.ChainID, "looked_up", looked, "resolved", resolved)
			}
			e.lastSigBlock = latest
		}
	}
	if latest > e.lastLabelBlock {
		processed, verified, err := e.FetchLabels(ctx, e.lastLabelBlock+1, latest, 20)
		if err != nil {
			slog.Warn("enrich: label fetch failed", "chain_id", e.ChainID, "error", err)
		} else {
			if processed > 0 {
				slog.Info("enrich: labelled contracts", "chain_id", e.ChainID, "processed", processed, "verified", verified)
			}
			// Keep the window open while the batch cap leaves work behind.
			if processed < 20 {
				e.lastLabelBlock = latest
			}
		}
	}
}
