package api

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"icicle/pkg/enrich"
)

// AddressLabel is the label shape embedded on transactions and returned by
// the labels endpoint.
type AddressLabel struct {
	Name           string  `json:"name" example:"LFJ Router (v1)"`
	Kind           string  `json:"kind" example:"router"`
	Protocol       *string `json:"protocol" example:"LFJ"`
	IsContract     bool    `json:"is_contract" example:"true"`
	Verified       bool    `json:"verified" example:"true"`
	Implementation *string `json:"implementation" example:"0x..."`
}

// enrichState is the API's in-memory copy of the enrichment tables. Request
// handlers only ever read from these maps; a background loop reloads them
// from ClickHouse so no request touches the tables (or a third party).
type enrichState struct {
	mu     sync.RWMutex
	sigs   map[[4]byte][]enrich.Candidate
	labels map[uint32]map[string][]enrich.Label
	tokens map[uint32]map[string]enrich.TokenInfo
	loaded time.Time
}

func newEnrichState() *enrichState {
	return &enrichState{
		sigs:   map[[4]byte][]enrich.Candidate{},
		labels: map[uint32]map[string][]enrich.Label{},
		tokens: map[uint32]map[string]enrich.TokenInfo{},
	}
}

// StartEnrichmentRefresh loads the enrichment maps now and then every
// interval until ctx is cancelled. Called by the api command, not by
// NewServer, so unit tests can run against a mock connection.
func (s *Server) StartEnrichmentRefresh(ctx context.Context, interval time.Duration) {
	go func() {
		for {
			if err := s.RefreshEnrichment(ctx); err != nil {
				slog.Warn("enrichment refresh failed", "error", err)
			}
			t := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	}()
}

// RefreshEnrichment reloads signatures, labels and token names from ClickHouse.
func (s *Server) RefreshEnrichment(ctx context.Context) error {
	start := time.Now()
	sigs, err := enrich.LoadSignatureCandidates(ctx, s.conn)
	if err != nil {
		return err
	}
	labels, err := enrich.LoadLabels(ctx, s.conn)
	if err != nil {
		return err
	}
	tokens, err := enrich.LoadTokenNames(ctx, s.conn)
	if err != nil {
		return err
	}
	s.enrichment.mu.Lock()
	s.enrichment.sigs, s.enrichment.labels, s.enrichment.tokens = sigs, labels, tokens
	s.enrichment.loaded = time.Now()
	s.enrichment.mu.Unlock()
	nLabels := 0
	for _, m := range labels {
		nLabels += len(m)
	}
	slog.Info("enrichment maps loaded", "selectors", len(sigs), "labels", nLabels, "tokens", countTokens(tokens), "took", time.Since(start).Round(time.Millisecond))
	return nil
}

func countTokens(m map[uint32]map[string]enrich.TokenInfo) int {
	n := 0
	for _, t := range m {
		n += len(t)
	}
	return n
}

// setEnrichmentForTest replaces the maps directly (unit tests).
func (s *Server) setEnrichmentForTest(sigs map[[4]byte][]enrich.Candidate, labels map[uint32]map[string][]enrich.Label, tokens map[uint32]map[string]enrich.TokenInfo) {
	s.enrichment.mu.Lock()
	defer s.enrichment.mu.Unlock()
	if sigs != nil {
		s.enrichment.sigs = sigs
	}
	if labels != nil {
		s.enrichment.labels = labels
	}
	if tokens != nil {
		s.enrichment.tokens = tokens
	}
}

// methodFor resolves the first 4 bytes of calldata. Returns nil when there is
// no calldata or the selector is unknown, as the frontend contract requires.
func (s *Server) methodFor(selector []byte, inputLen uint64) *enrich.Method {
	if len(selector) < 4 || inputLen < 4 {
		return nil
	}
	var sel [4]byte
	copy(sel[:], selector[:4])
	s.enrichment.mu.RLock()
	cands := s.enrichment.sigs[sel]
	s.enrichment.mu.RUnlock()
	sig, ok := enrich.ChooseSignature(cands, int(inputLen))
	if !ok {
		return nil
	}
	return &enrich.Method{Selector: enrich.SelectorHex(sel), Name: enrich.FunctionName(sig), Signature: sig}
}

// rawLabelFor merges every source we have for an address. addr is 0x-prefixed
// hex in any case; nil when nothing is known.
func (s *Server) rawLabelFor(chainID uint32, addr string) *enrich.Label {
	if addr == "" {
		return nil
	}
	key := strings.ToLower(addr)
	s.enrichment.mu.RLock()
	var cands []enrich.Label
	if m := s.enrichment.labels[chainID]; m != nil {
		cands = append(cands, m[key]...)
	}
	if t := s.enrichment.tokens[chainID]; t != nil {
		if info, ok := t[key]; ok {
			cands = append(cands, enrich.TokenLabel(key, info))
		}
	}
	s.enrichment.mu.RUnlock()
	return enrich.MergeLabels(cands)
}

// labelFor is rawLabelFor in the API's output shape.
func (s *Server) labelFor(chainID uint32, addr string) *AddressLabel {
	return toAddressLabel(s.rawLabelFor(chainID, addr))
}

func toAddressLabel(l *enrich.Label) *AddressLabel {
	if l == nil {
		return nil
	}
	out := &AddressLabel{Name: l.Name, Kind: l.Kind, IsContract: l.IsContract, Verified: l.Verified}
	if l.Protocol != "" {
		p := l.Protocol
		out.Protocol = &p
	}
	if l.Implementation != "" {
		impl := l.Implementation
		out.Implementation = &impl
	}
	return out
}
