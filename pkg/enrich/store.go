package enrich

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"icicle/pkg/chwrapper"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// --- function_signatures -------------------------------------------------

// UpsertSignatures writes rows to function_signatures. The table is a
// ReplacingMergeTree keyed by (selector, signature) so re-inserting is idempotent.
func UpsertSignatures(ctx context.Context, conn driver.Conn, rows []SignatureRow) error {
	if len(rows) == 0 {
		return nil
	}
	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	batch, err := conn.PrepareBatch(wctx, `INSERT INTO function_signatures (selector, signature, name, source, priority, resolved_at)`)
	if err != nil {
		return err
	}
	defer func() { _ = batch.Abort() }()
	now := time.Now().UTC()
	for _, r := range rows {
		if err := batch.Append(r.Selector, r.Signature, r.Name, r.Source, r.Priority, now); err != nil {
			return err
		}
	}
	return batch.Send()
}

// KnownSelectors returns every selector that has at least one row (resolved or
// negative-cached) together with the newest resolved_at.
func KnownSelectors(ctx context.Context, conn driver.Conn) (map[[4]byte]time.Time, error) {
	rctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	rows, err := conn.Query(rctx, `SELECT selector, max(resolved_at) FROM function_signatures GROUP BY selector`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[[4]byte]time.Time)
	for rows.Next() {
		var sel [4]byte
		var at time.Time
		if err := rows.Scan(&sel, &at); err != nil {
			return nil, err
		}
		out[sel] = at
	}
	return out, rows.Err()
}

// SelectorsInWindow returns distinct selectors used by transactions in the
// block range, most frequent first.
func SelectorsInWindow(ctx context.Context, conn driver.Conn, chainID uint32, fromBlock, toBlock uint64, limit int) ([][4]byte, error) {
	rctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	rows, err := conn.Query(rctx, `
		SELECT substring(input, 1, 4) AS sel, count() AS c
		FROM raw_txs
		WHERE chain_id = ? AND block_number BETWEEN ? AND ? AND length(input) >= 4
		GROUP BY sel
		ORDER BY c DESC
		LIMIT ?`, chainID, fromBlock, toBlock, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][4]byte
	for rows.Next() {
		var sel []byte
		var c uint64
		if err := rows.Scan(&sel, &c); err != nil {
			return nil, err
		}
		if len(sel) != 4 {
			continue
		}
		var fixed [4]byte
		copy(fixed[:], sel)
		out = append(out, fixed)
	}
	return out, rows.Err()
}

// StaleUnknownSelectors returns negative-cached selectors (signature = ”) whose
// lookup is older than maxAge, so they can be retried weekly.
func StaleUnknownSelectors(ctx context.Context, conn driver.Conn, maxAge time.Duration, limit int) ([][4]byte, error) {
	rctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	rows, err := conn.Query(rctx, `
		SELECT selector FROM (SELECT * FROM function_signatures FINAL) s
		WHERE signature = '' AND resolved_at < ?
		  AND selector NOT IN (SELECT selector FROM function_signatures WHERE signature != '')
		LIMIT ?`, time.Now().UTC().Add(-maxAge), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][4]byte
	for rows.Next() {
		var sel [4]byte
		if err := rows.Scan(&sel); err != nil {
			return nil, err
		}
		out = append(out, sel)
	}
	return out, rows.Err()
}

// ResolveSelectors looks selectors up on OpenChain in batches and stores the
// results, writing a negative-cache row for every selector with no match.
// Returns the number of selectors that resolved to at least one signature.
func ResolveSelectors(ctx context.Context, conn driver.Conn, oc *OpenChainClient, selectors [][4]byte) (int, error) {
	resolved := 0
	for start := 0; start < len(selectors); start += OpenChainBatchSize {
		end := start + OpenChainBatchSize
		if end > len(selectors) {
			end = len(selectors)
		}
		res, err := oc.Lookup(ctx, selectors[start:end])
		if err != nil {
			return resolved, err
		}
		var rows []SignatureRow
		for sel, hits := range res {
			if len(hits) == 0 {
				rows = append(rows, SignatureRow{Selector: sel, Signature: "", Name: "", Source: "openchain", Priority: PriorityOpenChain})
				continue
			}
			resolved++
			rows = append(rows, hits...)
		}
		if err := UpsertSignatures(ctx, conn, rows); err != nil {
			return resolved, err
		}
	}
	return resolved, nil
}

// LoadSignatureCandidates reads every resolved signature for the API's
// in-memory map.
func LoadSignatureCandidates(ctx context.Context, conn driver.Conn) (map[[4]byte][]Candidate, error) {
	rctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	rows, err := conn.Query(rctx, `
		SELECT selector, signature, min(priority)
		FROM function_signatures
		WHERE signature != ''
		GROUP BY selector, signature`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[[4]byte][]Candidate)
	for rows.Next() {
		var sel [4]byte
		var sig string
		var prio uint8
		if err := rows.Scan(&sel, &sig, &prio); err != nil {
			return nil, err
		}
		out[sel] = append(out[sel], Candidate{Signature: sig, Priority: prio})
	}
	return out, rows.Err()
}

// --- address_labels ------------------------------------------------------

// UpsertLabels writes labels for one chain. ReplacingMergeTree keyed by
// (chain_id, address, source) makes this idempotent per source.
func UpsertLabels(ctx context.Context, conn driver.Conn, chainID uint32, labels []Label) error {
	if len(labels) == 0 {
		return nil
	}
	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	batch, err := conn.PrepareBatch(wctx, `INSERT INTO address_labels (chain_id, address, name, kind, protocol, is_contract, verified, implementation, source, updated_at)`)
	if err != nil {
		return err
	}
	defer func() { _ = batch.Abort() }()
	now := time.Now().UTC()
	for _, l := range labels {
		addr, err := addrBytes(l.Address)
		if err != nil {
			return err
		}
		// Nullable(FixedString(20)): the driver accepts *string for FixedString
		// (nil = NULL) but not *[20]byte, so pass the raw bytes as a string.
		var impl *string
		if l.Implementation != "" {
			b, err := addrBytes(l.Implementation)
			if err != nil {
				return err
			}
			s := string(b[:])
			impl = &s
		}
		if err := batch.Append(chainID, addr, l.Name, l.Kind, l.Protocol, l.IsContract, l.Verified, impl, l.Source, now); err != nil {
			return err
		}
	}
	return batch.Send()
}

// KnownLabelAddresses returns every address with any row (including negative
// cache) and the newest updated_at, for the fetcher's skip list.
func KnownLabelAddresses(ctx context.Context, conn driver.Conn, chainID uint32) (map[string]time.Time, error) {
	rctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	rows, err := conn.Query(rctx, `SELECT address, max(updated_at) FROM address_labels WHERE chain_id = ? GROUP BY address`, chainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]time.Time)
	for rows.Next() {
		var addr [20]byte
		var at time.Time
		if err := rows.Scan(&addr, &at); err != nil {
			return nil, err
		}
		out["0x"+hex.EncodeToString(addr[:])] = at
	}
	return out, rows.Err()
}

// ContractCandidates returns the addresses that received calldata-carrying
// transactions in the block range, most active first. EOAs are filtered out
// later by eth_getCode.
func ContractCandidates(ctx context.Context, conn driver.Conn, chainID uint32, fromBlock, toBlock uint64, limit int) ([]string, error) {
	rctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	rows, err := conn.Query(rctx, `
		SELECT to, count() AS c
		FROM raw_txs
		WHERE chain_id = ? AND block_number BETWEEN ? AND ? AND to IS NOT NULL AND length(input) >= 4
		GROUP BY to
		ORDER BY c DESC
		LIMIT ?`, chainID, fromBlock, toBlock, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var to []byte
		var c uint64
		if err := rows.Scan(&to, &c); err != nil {
			return nil, err
		}
		if len(to) == 20 {
			out = append(out, "0x"+hex.EncodeToString(to))
		}
	}
	return out, rows.Err()
}

// LoadLabels reads every named label for the API's in-memory map:
// chain -> address -> labels from each source.
func LoadLabels(ctx context.Context, conn driver.Conn) (map[uint32]map[string][]Label, error) {
	rctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	rows, err := conn.Query(rctx, `
		SELECT chain_id, address, name, kind, protocol, is_contract, verified, implementation, source
		FROM (SELECT * FROM address_labels FINAL) l
		WHERE name != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uint32]map[string][]Label)
	for rows.Next() {
		var chainID uint32
		var addr [20]byte
		var impl *string
		var l Label
		if err := rows.Scan(&chainID, &addr, &l.Name, &l.Kind, &l.Protocol, &l.IsContract, &l.Verified, &impl, &l.Source); err != nil {
			return nil, err
		}
		l.Address = "0x" + hex.EncodeToString(addr[:])
		if impl != nil && len(*impl) == 20 {
			l.Implementation = "0x" + hex.EncodeToString([]byte(*impl))
		}
		if out[chainID] == nil {
			out[chainID] = make(map[string][]Label)
		}
		out[chainID][l.Address] = append(out[chainID][l.Address], l)
	}
	return out, rows.Err()
}

// TokenInfo is the token_metadata subset used for token-metadata labels.
type TokenInfo struct {
	Name   string
	Symbol string
}

// LoadTokenNames reads token_metadata (name/symbol only) for the API's
// in-memory map: chain -> address -> info.
func LoadTokenNames(ctx context.Context, conn driver.Conn) (map[uint32]map[string]TokenInfo, error) {
	rctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	rows, err := conn.Query(rctx, `
		SELECT chain_id, token, name, symbol
		FROM (SELECT * FROM token_metadata FINAL) t
		WHERE name != '' OR symbol != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uint32]map[string]TokenInfo)
	for rows.Next() {
		var chainID uint32
		var addr [20]byte
		var info TokenInfo
		if err := rows.Scan(&chainID, &addr, &info.Name, &info.Symbol); err != nil {
			return nil, err
		}
		if out[chainID] == nil {
			out[chainID] = make(map[string]TokenInfo)
		}
		out[chainID]["0x"+hex.EncodeToString(addr[:])] = info
	}
	return out, rows.Err()
}

// TokenLabel converts token metadata into a token-metadata-source label.
func TokenLabel(addr string, info TokenInfo) Label {
	name := info.Name
	if name == "" {
		name = info.Symbol
	} else if info.Symbol != "" && info.Symbol != info.Name {
		name = info.Name + " (" + info.Symbol + ")"
	}
	return Label{Address: addr, Name: name, Kind: "token", IsContract: true, Source: SourceTokenMetadata}
}

// BlockAtOrAfter returns the first indexed block number whose block_time is at
// or after t, used to turn "last N days" into a block window once per run.
func BlockAtOrAfter(ctx context.Context, conn driver.Conn, chainID uint32, t time.Time) (uint64, error) {
	rctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	var bn uint64
	err := conn.QueryRow(rctx, `SELECT toUInt64(min(block_number)) FROM raw_blocks WHERE chain_id = ? AND block_time >= ?`, chainID, t.UTC()).Scan(&bn)
	return bn, err
}

// LatestBlock returns the highest indexed block for the chain.
func LatestBlock(ctx context.Context, conn driver.Conn, chainID uint32) (uint64, error) {
	rctx, cancel := chwrapper.ReadContext(ctx)
	defer cancel()
	var bn uint64
	err := conn.QueryRow(rctx, `SELECT toUInt64(max(block_number)) FROM raw_blocks WHERE chain_id = ?`, chainID).Scan(&bn)
	return bn, err
}

func addrBytes(s string) ([20]byte, error) {
	var out [20]byte
	norm, ok := NormalizeAddress(s)
	if !ok {
		return out, fmt.Errorf("bad address %q", s)
	}
	b, err := hex.DecodeString(norm[2:])
	if err != nil {
		return out, err
	}
	copy(out[:], b)
	return out, nil
}
