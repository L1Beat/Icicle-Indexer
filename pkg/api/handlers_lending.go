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
		) WHERE debt > 0
		ORDER BY liq DESC, hf ASC, debt DESC
		LIMIT ? OFFSET ?
	`, strings.Join(conds, " AND "))
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

	rows, err := s.conn.Query(ctx, `
		SELECT protocol,
			countIf(debt > 0) AS open_positions,
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
