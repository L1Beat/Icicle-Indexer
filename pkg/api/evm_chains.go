package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// indexedChains caches the EVM chain IDs the indexer syncs, so the
// /data/evm/{chainId} and /metrics/evm/{chainId} routes can answer an
// unindexed chain with one clear 404 instead of empty data or zeros.
type indexedChains struct {
	lookup func(ctx context.Context) (map[uint32]bool, error)
	ttl    time.Duration

	mu      sync.Mutex
	chains  map[uint32]bool
	fetched time.Time
}

func newIndexedChains(lookup func(ctx context.Context) (map[uint32]bool, error)) *indexedChains {
	return &indexedChains{lookup: lookup, ttl: time.Minute}
}

// indexed reports whether chainID is synced. ok is false when the set could
// not be loaded; callers then let the request through rather than reject a
// chain that may be indexed.
func (c *indexedChains) indexed(ctx context.Context, chainID uint32) (indexed, ok bool) {
	if c.lookup == nil {
		return false, false // no chain list (tests): let every chain through
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.chains == nil || time.Since(c.fetched) > c.ttl {
		chains, err := c.lookup(ctx)
		if err != nil {
			slog.Warn("Failed to load indexed EVM chains", "error", err)
			if c.chains == nil {
				return false, false
			}
		} else {
			c.chains, c.fetched = chains, time.Now()
		}
	}
	return c.chains[chainID], true
}

// syncedEVMChains reads the chains with an EVM sync watermark.
func (s *Server) syncedEVMChains(ctx context.Context) (map[uint32]bool, error) {
	rows, err := s.conn.Query(ctx, `SELECT chain_id FROM sync_watermark WHERE chain_id > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chains := map[uint32]bool{}
	for rows.Next() {
		var id uint32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		chains[id] = true
	}
	return chains, rows.Err()
}

// requireIndexedChain answers 404 "Chain not indexed" for an EVM chain the
// indexer does not sync, before the handler runs any query. An unparseable
// chain ID is left to the handler, which returns its usual 400.
func (s *Server) requireIndexedChain(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if chainID, err := getChainIDFromPath(r); err == nil {
			if indexed, ok := s.evmChains.indexed(r.Context(), chainID); ok && !indexed {
				writeAPIError(w, http.StatusNotFound, ErrNotFound, "Chain not indexed")
				return
			}
		}
		next(w, r)
	}
}
