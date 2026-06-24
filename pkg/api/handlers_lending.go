package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"icicle/pkg/lending"
)

// Lending liquidation-risk feed. Positions are served from lending_positions with
// argMax over updated_at so pre-merge ReplacingMergeTree duplicates never return
// stale health (rule 2).

type lendingLeg struct {
	Asset     string `json:"asset"`
	Symbol    string `json:"symbol,omitempty"`
	Amount    string `json:"amount"`
	BaseValue string `json:"base_value"`
}

type lendingEstimate struct {
	CollateralAsset      string `json:"collateral_asset"`
	DebtAsset            string `json:"debt_asset"`
	CloseFactorBps       uint16 `json:"close_factor_bps"`
	LiquidationBonusBps  uint16 `json:"liquidation_bonus_bps"`
	MaxDebtRepaidBase    string `json:"max_debt_repaid_base"`
	SeizedCollateralBase string `json:"seized_collateral_base"`
	GrossBonusBase       string `json:"gross_bonus_base"`
	SlippageModeled      bool   `json:"slippage_modeled"`
	Note                 string `json:"note"`
}

type lendingPosition struct {
	Account        string           `json:"account"`
	Protocol       string           `json:"protocol"`
	HealthFactor   string           `json:"health_factor"`
	Liquidatable   bool             `json:"liquidatable"`
	CollateralBase string           `json:"collateral_base"`
	DebtBase       string           `json:"debt_base"`
	ShortfallBase  string           `json:"shortfall_base,omitempty"`
	Tier           string           `json:"tier"`
	Collateral     []lendingLeg     `json:"collateral"`
	Debt           []lendingLeg     `json:"debt"`
	Liquidation    *lendingEstimate `json:"liquidation,omitempty"`
	BlockNumber    uint32           `json:"block_number"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

// assetMeta is per-asset parameter metadata loaded for valuation and sizing.
type assetMeta struct {
	symbol   string
	bonusBps uint16
}

type globalMeta struct {
	closeFactorBps    uint16
	incentiveBps      uint16
	smallPositionBase *big.Int
}

// handleLendingPositions returns positions ranked by liquidation proximity,
// optionally filtered by protocol or asset.
func (s *Server) handleLendingPositions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}
	limit, offset := getPagination(r)
	protocol := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("protocol")))
	asset := normalizeHexAddr(r.URL.Query().Get("asset"))

	// Opt-in dust filter: only positions whose debt in the oracle base currency is
	// greater than this value. Validated as a base-10 integer so it is
	// injection-safe. Defaults to 0, the existing debt > 0 behavior.
	debtFloor := "0"
	if v := strings.TrimSpace(r.URL.Query().Get("min_debt_base")); v != "" {
		if _, ok := new(big.Int).SetString(v, 10); ok {
			debtFloor = v
		}
	}

	var conds []string
	args := []interface{}{chainID}
	conds = append(conds, "chain_id = ?")
	if protocol != "" {
		conds = append(conds, "protocol = ?")
		args = append(args, protocol)
	}
	if asset != "" {
		conds = append(conds, fmt.Sprintf("account IN (SELECT account FROM lending_position_assets WHERE chain_id = ? AND asset = unhex('%s'))", asset))
		args = append(args, chainID)
	}

	query := fmt.Sprintf(`
		SELECT account, protocol, hf, coll, debt, short, liq, tier, blk, upd FROM (
			SELECT account, protocol,
				argMax(health_factor, updated_at) AS hf,
				argMax(collateral_base, updated_at) AS coll,
				argMax(debt_base, updated_at) AS debt,
				argMax(shortfall_base, updated_at) AS short,
				argMax(liquidatable, updated_at) AS liq,
				argMax(tier, updated_at) AS tier,
				argMax(block_number, updated_at) AS blk,
				max(updated_at) AS upd
			FROM lending_positions
			WHERE %s
			GROUP BY account, protocol
		) WHERE debt > %s
		ORDER BY liq DESC, hf ASC, debt DESC
		LIMIT ? OFFSET ?
	`, strings.Join(conds, " AND "), debtFloor)
	args = append(args, limit+1, offset)

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	var positions []lendingPosition
	var accounts []string
	for rows.Next() {
		var acc [20]byte
		var proto, tier string
		var hf, coll, debt, short *big.Int
		var liq bool
		var blk uint32
		var upd time.Time
		if err := rows.Scan(&acc, &proto, &hf, &coll, &debt, &short, &liq, &tier, &blk, &upd); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		address := "0x" + hex.EncodeToString(acc[:])
		positions = append(positions, lendingPosition{
			Account:        address,
			Protocol:       proto,
			HealthFactor:   bigStr(hf),
			Liquidatable:   liq,
			CollateralBase: bigStr(coll),
			DebtBase:       bigStr(debt),
			ShortfallBase:  bigStrOmitZero(short),
			Tier:           tier,
			BlockNumber:    blk,
			UpdatedAt:      upd,
		})
		accounts = append(accounts, address)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}

	positions, hasMore := trimLendingResults(positions, limit)
	accounts = accounts[:len(positions)]

	legs := s.loadLegs(ctx, chainID, accounts)
	params := s.loadAssetMeta(ctx, chainID)
	globals := s.loadGlobalMeta(ctx, chainID)
	for i := range positions {
		s.enrich(&positions[i], legs, params, globals)
	}

	meta := &Meta{Limit: limit, Offset: offset, HasMore: hasMore}
	writeJSON(w, http.StatusOK, Response{Data: positions, Meta: meta})
}

// handleLendingAccount returns every position held by one account.
func (s *Server) handleLendingAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}
	account := normalizeHexAddr(r.PathValue("account"))
	if account == "" {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid account address")
		return
	}

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT account, protocol, hf, coll, debt, short, liq, tier, blk, upd FROM (
			SELECT account, protocol,
				argMax(health_factor, updated_at) AS hf,
				argMax(collateral_base, updated_at) AS coll,
				argMax(debt_base, updated_at) AS debt,
				argMax(shortfall_base, updated_at) AS short,
				argMax(liquidatable, updated_at) AS liq,
				argMax(tier, updated_at) AS tier,
				argMax(block_number, updated_at) AS blk,
				max(updated_at) AS upd
			FROM lending_positions
			WHERE chain_id = ? AND account = unhex('%s')
			GROUP BY account, protocol
		)
	`, account), chainID)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	var positions []lendingPosition
	addr := "0x" + account
	for rows.Next() {
		var acc [20]byte
		var proto, tier string
		var hf, coll, debt, short *big.Int
		var liq bool
		var blk uint32
		var upd time.Time
		if err := rows.Scan(&acc, &proto, &hf, &coll, &debt, &short, &liq, &tier, &blk, &upd); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		positions = append(positions, lendingPosition{
			Account: addr, Protocol: proto, HealthFactor: bigStr(hf), Liquidatable: liq,
			CollateralBase: bigStr(coll), DebtBase: bigStr(debt), ShortfallBase: bigStrOmitZero(short),
			Tier: tier, BlockNumber: blk, UpdatedAt: upd,
		})
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}
	if len(positions) == 0 {
		writeNotFoundError(w, "Lending positions")
		return
	}

	legs := s.loadLegs(ctx, chainID, []string{addr})
	params := s.loadAssetMeta(ctx, chainID)
	globals := s.loadGlobalMeta(ctx, chainID)
	for i := range positions {
		s.enrich(&positions[i], legs, params, globals)
	}
	writeJSON(w, http.StatusOK, Response{Data: positions})
}

// handleLendingStats returns headline counts per protocol.
func (s *Server) handleLendingStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	// A position is open if it carries debt or is flagged liquidatable. Benqi cold
	// sweeps are summary-only, so a liquidatable account can have debt_base 0 until
	// a detail read lands; counting it as open keeps liquidatable a subset of open.
	rows, err := s.conn.Query(ctx, `
		SELECT protocol,
			countIf(debt > 0 OR liq) AS open_positions,
			countIf(liq) AS liquidatable
		FROM (
			SELECT account, protocol,
				argMax(debt_base, updated_at) AS debt,
				argMax(liquidatable, updated_at) AS liq
			FROM lending_positions
			WHERE chain_id = ?
			GROUP BY account, protocol
		)
		GROUP BY protocol
	`, chainID)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	type stat struct {
		Protocol      string `json:"protocol"`
		OpenPositions uint64 `json:"open_positions"`
		Liquidatable  uint64 `json:"liquidatable"`
	}
	var stats []stat
	for rows.Next() {
		var st stat
		if err := rows.Scan(&st.Protocol, &st.OpenPositions, &st.Liquidatable); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		stats = append(stats, st)
	}
	writeJSON(w, http.StatusOK, Response{Data: stats})
}

// Health-factor histogram bounds (1e18-scaled). Fixed buckets so the dashboard can
// chart the full distribution, not just the loaded risk tail.
const (
	hfWAD = "1000000000000000000" // 1.0
	hf110 = "1100000000000000000" // 1.1
	hf125 = "1250000000000000000" // 1.25
	hf150 = "1500000000000000000" // 1.5
	hf200 = "2000000000000000000" // 2.0
)

type riskBucket struct {
	Range string `json:"range"`
	Count uint64 `json:"count"`
}

type riskAsset struct {
	Asset          string `json:"asset"`
	Symbol         string `json:"symbol,omitempty"`
	CollateralBase string `json:"collateral_base"`
	DebtBase       string `json:"debt_base"`
}

type riskStat struct {
	Protocol      string `json:"protocol"`
	MinDebtBase   string `json:"min_debt_base"`
	OpenPositions uint64 `json:"open_positions"`
	Liquidatable  uint64 `json:"liquidatable"`
	BadDebt       struct {
		Count     uint64 `json:"count"`
		TotalBase string `json:"total_base"`
	} `json:"bad_debt"`
	NearLiquidation struct {
		Count uint64 `json:"count"`
		HFMax string `json:"hf_max"`
	} `json:"near_liquidation"`
	ValueAtRisk struct {
		CollateralBase string `json:"collateral_base"`
		DebtBase       string `json:"debt_base"`
	} `json:"value_at_risk"`
	HFHistogram []riskBucket `json:"hf_histogram"`
	ByAsset     []riskAsset  `json:"by_asset"`
}

// handleLendingRisk returns server-side aggregate risk stats per protocol (and per
// asset), all gated by a min_debt_base debt floor so the dust-inclusive counts the
// overview shows can be replaced by the real, dust-free numbers. Computed in two
// GROUP BY passes over the argMax-deduped current state, never a plain SELECT, so
// pre-merge ReplacingMergeTree duplicates cannot return stale health (rule 2).
func (s *Server) handleLendingRisk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}
	protocol := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("protocol")))
	floor := validBigParam(r, "min_debt_base", "0")
	nearMax := validBigParam(r, "near_hf_max", hf110)

	conds := "chain_id = ?"
	args := []interface{}{chainID}
	if protocol != "" {
		conds += " AND protocol = ?"
		args = append(args, protocol)
	}

	// Pass 1: per-protocol aggregates over the deduped current positions. The debt
	// floor (and all HF bounds) are validated base-10 integers, so they are
	// injection-safe to inline; chain_id and protocol stay bound parameters.
	aggQ := fmt.Sprintf(`
		SELECT protocol,
			countIf(debt > %[1]s) AS open_positions,
			countIf(liq AND debt > %[1]s) AS liquidatable,
			countIf(coll < debt AND debt > %[1]s) AS bad_debt_count,
			sumIf(debt - coll, coll < debt AND debt > %[1]s) AS bad_debt_total,
			countIf(NOT liq AND hf >= %[2]s AND hf < %[3]s AND debt > %[1]s) AS near_count,
			sumIf(coll, (liq OR hf < %[3]s) AND debt > %[1]s) AS var_coll,
			sumIf(debt, (liq OR hf < %[3]s) AND debt > %[1]s) AS var_debt,
			countIf(hf < %[2]s AND debt > %[1]s) AS b0,
			countIf(hf >= %[2]s AND hf < %[4]s AND debt > %[1]s) AS b1,
			countIf(hf >= %[4]s AND hf < %[5]s AND debt > %[1]s) AS b2,
			countIf(hf >= %[5]s AND hf < %[6]s AND debt > %[1]s) AS b3,
			countIf(hf >= %[6]s AND hf < %[7]s AND debt > %[1]s) AS b4,
			countIf(hf >= %[7]s AND debt > %[1]s) AS b5
		FROM (
			SELECT account, protocol,
				argMax(health_factor, updated_at) AS hf,
				argMax(collateral_base, updated_at) AS coll,
				argMax(debt_base, updated_at) AS debt,
				argMax(liquidatable, updated_at) AS liq
			FROM lending_positions
			WHERE %[8]s
			GROUP BY account, protocol
		)
		GROUP BY protocol
		ORDER BY protocol
	`, floor, hfWAD, nearMax, hf110, hf125, hf150, hf200, conds)

	rows, err := s.conn.Query(ctx, aggQ, args...)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	statsByProto := map[string]*riskStat{}
	var order []string
	for rows.Next() {
		var st riskStat
		var badTotal, varColl, varDebt *big.Int
		var b [6]uint64
		if err := rows.Scan(&st.Protocol, &st.OpenPositions, &st.Liquidatable,
			&st.BadDebt.Count, &badTotal, &st.NearLiquidation.Count, &varColl, &varDebt,
			&b[0], &b[1], &b[2], &b[3], &b[4], &b[5]); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		st.MinDebtBase = floor
		st.BadDebt.TotalBase = bigStr(badTotal)
		st.NearLiquidation.HFMax = nearMax
		st.ValueAtRisk.CollateralBase = bigStr(varColl)
		st.ValueAtRisk.DebtBase = bigStr(varDebt)
		st.HFHistogram = []riskBucket{
			{"<1.0", b[0]}, {"1.0-1.1", b[1]}, {"1.1-1.25", b[2]},
			{"1.25-1.5", b[3]}, {"1.5-2.0", b[4]}, {">=2.0", b[5]},
		}
		st.ByAsset = []riskAsset{}
		statsByProto[st.Protocol] = &st
		order = append(order, st.Protocol)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}

	s.attachRiskByAsset(ctx, chainID, conds, args, floor, statsByProto)

	out := make([]riskStat, 0, len(order))
	for _, p := range order {
		out = append(out, *statsByProto[p])
	}
	writeJSON(w, http.StatusOK, Response{Data: out})
}

// attachRiskByAsset fills the per-asset collateral/debt exposure for each protocol,
// restricted to the dust-free open set (positions whose total debt clears the
// floor), and resolves the asset symbol (covering Benqi qiToken markets too).
func (s *Server) attachRiskByAsset(ctx context.Context, chainID uint32, conds string, args []interface{}, floor string, statsByProto map[string]*riskStat) {
	q := fmt.Sprintf(`
		SELECT protocol, asset, side, sum(val) AS total_base FROM (
			SELECT account, protocol, asset, side, argMax(base_value, updated_at) AS val
			FROM lending_position_assets
			WHERE %[1]s
			GROUP BY account, protocol, asset, side
		)
		WHERE val > 0 AND (protocol, account) IN (
			SELECT protocol, account FROM (
				SELECT account, protocol, argMax(debt_base, updated_at) AS debt
				FROM lending_positions WHERE %[1]s GROUP BY account, protocol
			) WHERE debt > %[2]s
		)
		GROUP BY protocol, asset, side
		ORDER BY total_base DESC
	`, conds, floor)

	// conds/args appear twice (legs subquery and the debt-floor subquery).
	dualArgs := append(append([]interface{}{}, args...), args...)
	rows, err := s.conn.Query(ctx, q, dualArgs...)
	if err != nil {
		return
	}
	defer rows.Close()

	symbols := s.loadAssetSymbols(ctx, chainID)
	// per protocol: asset -> *riskAsset, preserving size order.
	seen := map[string]map[string]*riskAsset{}
	order := map[string][]string{}
	for rows.Next() {
		var proto, side string
		var asset [20]byte
		var total *big.Int
		if err := rows.Scan(&proto, &asset, &side, &total); err != nil {
			return
		}
		addr := "0x" + hex.EncodeToString(asset[:])
		if seen[proto] == nil {
			seen[proto] = map[string]*riskAsset{}
		}
		ra := seen[proto][addr]
		if ra == nil {
			ra = &riskAsset{Asset: addr, Symbol: symbols[proto+"|"+addr], CollateralBase: "0", DebtBase: "0"}
			seen[proto][addr] = ra
			order[proto] = append(order[proto], addr)
		}
		if side == "debt" {
			ra.DebtBase = bigStr(total)
		} else {
			ra.CollateralBase = bigStr(total)
		}
	}
	for proto, addrs := range order {
		st := statsByProto[proto]
		if st == nil {
			continue
		}
		for _, addr := range addrs {
			st.ByAsset = append(st.ByAsset, *seen[proto][addr])
		}
	}
}

// loadAssetSymbols maps both underlying asset and Benqi qiToken market addresses to
// their symbol, so per-asset breakdowns label Benqi legs that arrive keyed by the
// qiToken market.
func (s *Server) loadAssetSymbols(ctx context.Context, chainID uint32) map[string]string {
	out := map[string]string{}
	rows, err := s.conn.Query(ctx, `
		SELECT protocol, asset, market, symbol
		FROM (SELECT * FROM lending_protocol_params FINAL)
		WHERE chain_id = ?
	`, chainID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var proto, symbol string
		var asset, market [20]byte
		if err := rows.Scan(&proto, &asset, &market, &symbol); err != nil {
			return out
		}
		if symbol == "" {
			continue
		}
		out[proto+"|0x"+hex.EncodeToString(asset[:])] = symbol
		if market != ([20]byte{}) {
			out[proto+"|0x"+hex.EncodeToString(market[:])] = symbol
		}
	}
	return out
}

// validBigParam returns a query param if it is a valid base-10 integer, else def.
func validBigParam(r *http.Request, name, def string) string {
	if v := strings.TrimSpace(r.URL.Query().Get(name)); v != "" {
		if _, ok := new(big.Int).SetString(v, 10); ok {
			return v
		}
	}
	return def
}

// handleLendingAlerts returns recent crossing events, newest first.
func (s *Server) handleLendingAlerts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}
	limit, offset := getPagination(r)

	rows, err := s.conn.Query(ctx, `
		SELECT account, protocol, kind, health_factor, collateral_base, debt_base, block_number, created_at
		FROM lending_alerts
		WHERE chain_id = ?
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, chainID, limit+1, offset)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	type alert struct {
		Account        string    `json:"account"`
		Protocol       string    `json:"protocol"`
		Kind           string    `json:"kind"`
		HealthFactor   string    `json:"health_factor"`
		CollateralBase string    `json:"collateral_base"`
		DebtBase       string    `json:"debt_base"`
		BlockNumber    uint32    `json:"block_number"`
		CreatedAt      time.Time `json:"created_at"`
	}
	var alerts []alert
	for rows.Next() {
		var acc [20]byte
		var a alert
		var hf, coll, debt *big.Int
		if err := rows.Scan(&acc, &a.Protocol, &a.Kind, &hf, &coll, &debt, &a.BlockNumber, &a.CreatedAt); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		a.Account = "0x" + hex.EncodeToString(acc[:])
		a.HealthFactor = bigStr(hf)
		a.CollateralBase = bigStr(coll)
		a.DebtBase = bigStr(debt)
		alerts = append(alerts, a)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}

	hasMore := len(alerts) > limit
	if hasMore {
		alerts = alerts[:limit]
	}
	writeJSON(w, http.StatusOK, Response{Data: alerts, Meta: &Meta{Limit: limit, Offset: offset, HasMore: hasMore}})
}

// --- enrichment helpers ---

func (s *Server) enrich(p *lendingPosition, legs map[string][]lendingLeg, params map[string]assetMeta, globals map[string]globalMeta) {
	key := p.Protocol + "|" + p.Account
	for _, leg := range legs[key+"|collateral"] {
		leg.Symbol = params[p.Protocol+"|"+leg.Asset].symbol
		p.Collateral = append(p.Collateral, leg)
	}
	for _, leg := range legs[key+"|debt"] {
		leg.Symbol = params[p.Protocol+"|"+leg.Asset].symbol
		p.Debt = append(p.Debt, leg)
	}
	p.Liquidation = buildEstimate(p, params, globals)
}

// buildEstimate sizes the largest collateral and debt legs into a gross-only
// liquidation estimate. Slippage is not modeled in v1 (rule 4).
func buildEstimate(p *lendingPosition, params map[string]assetMeta, globals map[string]globalMeta) *lendingEstimate {
	coll := largestLeg(p.Collateral)
	debt := largestLeg(p.Debt)
	if coll == nil || debt == nil {
		return nil
	}
	g := globals[p.Protocol]

	var bonusBps, closeFactorBps uint16
	switch lending.Protocol(p.Protocol) {
	case lending.ProtocolAaveV3:
		bonusBps = params[p.Protocol+"|"+coll.Asset].bonusBps
		closeFactorBps = lending.AaveCloseFactorBps(toBig(p.HealthFactor), toBig(p.DebtBase), g.smallPositionBase)
	case lending.ProtocolBenqi:
		bonusBps = g.incentiveBps
		closeFactorBps = g.closeFactorBps
	}

	est := lending.EstimateLiquidation(coll.Asset, debt.Asset, toBig(debt.BaseValue), toBig(coll.BaseValue), closeFactorBps, bonusBps)
	return &lendingEstimate{
		CollateralAsset:      est.CollateralAsset,
		DebtAsset:            est.DebtAsset,
		CloseFactorBps:       est.CloseFactorBps,
		LiquidationBonusBps:  est.LiquidationBonusBps,
		MaxDebtRepaidBase:    bigStr(est.MaxDebtRepaidBase),
		SeizedCollateralBase: bigStr(est.SeizedCollateralBase),
		GrossBonusBase:       bigStr(est.GrossBonusBase),
		SlippageModeled:      false,
		Note:                 "gross estimate, slippage not modeled (no DEX liquidity wired in this phase)",
	}
}

// loadLegs returns per-asset legs keyed by "protocol|account|side".
func (s *Server) loadLegs(ctx context.Context, chainID uint32, accounts []string) map[string][]lendingLeg {
	out := map[string][]lendingLeg{}
	if len(accounts) == 0 {
		return out
	}
	in := accountInList(accounts)
	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT account, protocol, asset, side, amt, val FROM (
			SELECT account, protocol, asset, side,
				argMax(amount, updated_at) AS amt,
				argMax(base_value, updated_at) AS val
			FROM lending_position_assets
			WHERE chain_id = ? AND account IN (%s)
			GROUP BY account, protocol, asset, side
		) WHERE amt > 0
	`, in), chainID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var acc, asset [20]byte
		var proto, side string
		var amt, val *big.Int
		if err := rows.Scan(&acc, &proto, &asset, &side, &amt, &val); err != nil {
			return out
		}
		key := proto + "|0x" + hex.EncodeToString(acc[:]) + "|" + side
		out[key] = append(out[key], lendingLeg{
			Asset: "0x" + hex.EncodeToString(asset[:]), Amount: bigStr(amt), BaseValue: bigStr(val),
		})
	}
	return out
}

func (s *Server) loadAssetMeta(ctx context.Context, chainID uint32) map[string]assetMeta {
	out := map[string]assetMeta{}
	rows, err := s.conn.Query(ctx, `
		SELECT protocol, asset, symbol, liquidation_bonus_bps
		FROM (SELECT * FROM lending_protocol_params FINAL)
		WHERE chain_id = ?
	`, chainID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var proto, symbol string
		var asset [20]byte
		var bonus uint16
		if err := rows.Scan(&proto, &asset, &symbol, &bonus); err != nil {
			return out
		}
		out[proto+"|0x"+hex.EncodeToString(asset[:])] = assetMeta{symbol: symbol, bonusBps: bonus}
	}
	return out
}

func (s *Server) loadGlobalMeta(ctx context.Context, chainID uint32) map[string]globalMeta {
	out := map[string]globalMeta{}
	rows, err := s.conn.Query(ctx, `
		SELECT protocol, close_factor_bps, liquidation_incentive_bps, small_position_base
		FROM (SELECT * FROM lending_protocol_globals FINAL)
		WHERE chain_id = ?
	`, chainID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var proto string
		var cf, inc uint16
		var small *big.Int
		if err := rows.Scan(&proto, &cf, &inc, &small); err != nil {
			return out
		}
		out[proto] = globalMeta{closeFactorBps: cf, incentiveBps: inc, smallPositionBase: small}
	}
	return out
}

// --- small helpers ---

func largestLeg(legs []lendingLeg) *lendingLeg {
	var best *lendingLeg
	var bestVal *big.Int
	for i := range legs {
		v := toBig(legs[i].BaseValue)
		if bestVal == nil || v.Cmp(bestVal) > 0 {
			bestVal = v
			best = &legs[i]
		}
	}
	return best
}

func trimLendingResults(p []lendingPosition, limit int) ([]lendingPosition, bool) {
	if len(p) > limit {
		return p[:limit], true
	}
	return p, false
}

func bigStr(n *big.Int) string {
	if n == nil {
		return "0"
	}
	return n.String()
}

func bigStrOmitZero(n *big.Int) string {
	if n == nil || n.Sign() == 0 {
		return ""
	}
	return n.String()
}

func toBig(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return big.NewInt(0)
	}
	return n
}

func normalizeHexAddr(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "0x")
	if len(s) != 40 {
		return ""
	}
	if _, err := hex.DecodeString(s); err != nil {
		return ""
	}
	return s
}

func accountInList(accounts []string) string {
	parts := make([]string, 0, len(accounts))
	for _, a := range accounts {
		h := strings.TrimPrefix(strings.ToLower(a), "0x")
		if len(h) == 40 {
			parts = append(parts, "unhex('"+h+"')")
		}
	}
	if len(parts) == 0 {
		return "unhex('')"
	}
	return strings.Join(parts, ", ")
}
