package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"icicle/pkg/pchainrpc"
)

// ValidatorDelegation is one delegation to a Primary Network validator.
type ValidatorDelegation struct {
	TxID        string    `json:"tx_id"`
	RewardOwner []string  `json:"reward_owner"` // P-Chain addresses, null if the tx carries none
	Stake       string    `json:"stake"`        // nAVAX
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	Active      bool      `json:"active"`
}

// DelegatorDelegation is one delegation made by a reward address.
type DelegatorDelegation struct {
	TxID      string    `json:"tx_id"`
	NodeID    string    `json:"node_id"`
	Stake     string    `json:"stake"` // nAVAX
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Active    bool      `json:"active"`
}

// DelegatorSummary aggregates a reward address's delegations.
type DelegatorSummary struct {
	ActiveCount         int        `json:"active_count"`
	ActiveStake         string     `json:"active_stake"`    // nAVAX
	ValidatorCount      int        `json:"validator_count"` // distinct nodes among active delegations
	FirstDelegationTime *time.Time `json:"first_delegation_time"`
}

// DelegatorMeta is Meta plus the address summary.
type DelegatorMeta struct {
	Meta
	Summary DelegatorSummary `json:"summary"`
}

// DelegatorResponse is Response with the summary-bearing meta.
type DelegatorResponse struct {
	Data []DelegatorDelegation `json:"data"`
	Meta DelegatorMeta         `json:"meta"`
}

type delegationRow struct {
	txID       string
	nodeID     string
	owners     []string
	stake      uint64
	start, end int64
}

var (
	delegationsCache   = newTTLCache(5 * time.Minute)
	delegatorCache     = newTTLCache(5 * time.Minute)
	delegatorPageCache = newTTLCache(5 * time.Minute)
)

// handleValidatorDelegations lists the delegations to a Primary Network validator
// @Summary List delegations to a validator
// @Description AddDelegator / AddPermissionlessDelegator transactions naming this node, Primary Network only. active=true returns running delegations, active=false ended ones, omitted returns both. Stake is a nAVAX string. Cached for 5 minutes.
// @Tags Data - Validators
// @Produce json
// @Param id path string true "NodeID (NodeID-...)"
// @Param active query bool false "Filter by running (true) or ended (false)"
// @Param sort query string false "stake, end_time or start_time" default(stake)
// @Param order query string false "desc or asc" default(desc)
// @Param limit query int false "Page size, max 500" default(20)
// @Param offset query int false "Offset" default(0)
// @Success 200 {object} Response{data=[]ValidatorDelegation}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/validators/{id}/delegations [get]
func (s *Server) handleValidatorDelegations(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if !strings.HasPrefix(nodeID, "NodeID-") {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "id must be a NodeID")
		return
	}
	active, less, ok := delegationParams(w, r.URL.Query())
	if !ok {
		return
	}
	limit, offset := getPaginationWithCap(r, 500)

	// Running delegations only need the last ~13 months of txs, so they get a
	// cheaper query than the full history.
	onlyRunning := active != nil && *active
	scope := "all"
	if onlyRunning {
		scope = "active"
	}
	v, err := delegationsCache.get(r.Context(), nodeID+"|"+scope, func(ctx context.Context) (interface{}, error) {
		return s.loadDelegations(ctx, "toString(tx_data.validator.nodeID) = ?", nodeID, onlyRunning)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}

	now := time.Now().Unix()
	rows := filterSortDelegations(v.([]delegationRow), active, less, now)
	page := []ValidatorDelegation{}
	for _, d := range pageOf(rows, limit, offset) {
		page = append(page, ValidatorDelegation{
			TxID:        d.txID,
			RewardOwner: d.owners,
			Stake:       strconv.FormatUint(d.stake, 10),
			StartTime:   time.Unix(d.start, 0).UTC(),
			EndTime:     time.Unix(d.end, 0).UTC(),
			Active:      d.end > now,
		})
	}

	writeJSON(w, http.StatusOK, Response{
		Data: page,
		Meta: &Meta{Total: int64(len(rows)), Limit: limit, Offset: offset, HasMore: offset+limit < len(rows)},
	})
}

// handleDelegatorDelegations lists the delegations paying rewards to an address
// @Summary List delegations by reward address
// @Description Primary Network AddDelegator / AddPermissionlessDelegator transactions whose reward owner includes this address, across all validators. meta.summary covers all of the address's delegations regardless of the active filter. Sorting and paging run in ClickHouse, so large staking services with millions of rows work. Stake is a nAVAX string. Cached for 5 minutes.
// @Tags Data - Validators
// @Produce json
// @Param address path string true "P-Chain address (P-avax1... or avax1...)"
// @Param active query bool false "Filter by running (true) or ended (false)"
// @Param sort query string false "stake, end_time or start_time" default(stake)
// @Param order query string false "desc or asc" default(desc)
// @Param limit query int false "Page size, max 500" default(20)
// @Param offset query int false "Offset" default(0)
// @Success 200 {object} DelegatorResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/delegators/{address}/delegations [get]
func (s *Server) handleDelegatorDelegations(w http.ResponseWriter, r *http.Request) {
	short, err := pchainrpc.ConvertPChainAddressToCB58(r.PathValue("address"))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "address must be a mainnet P-Chain address (P-avax1...)")
		return
	}
	active, _, ok := delegationParams(w, r.URL.Query()) // sorting runs in ClickHouse
	if !ok {
		return
	}
	limit, offset := getPaginationWithCap(r, 500)

	sortKey, order := r.URL.Query().Get("sort"), strings.ToLower(r.URL.Query().Get("order"))

	// Large staking services have millions of delegations, so paging, sorting and
	// the summary all run in ClickHouse rather than over a cached full list.
	sumV, err := delegatorCache.get(r.Context(), short, func(ctx context.Context) (interface{}, error) {
		return s.loadDelegatorSummary(ctx, short)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	sum := sumV.(delegatorTotals)

	key := fmt.Sprintf("%s|%v|%s|%s|%d|%d", short, boolKey(active), sortKey, order, limit, offset)
	pageV, err := delegatorPageCache.get(r.Context(), key, func(ctx context.Context) (interface{}, error) {
		return s.loadDelegatorPage(ctx, short, active, sortKey, order, limit, offset)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}

	now := time.Now().Unix()
	page := []DelegatorDelegation{}
	for _, d := range pageV.([]delegationRow) {
		page = append(page, DelegatorDelegation{
			TxID:      d.txID,
			NodeID:    d.nodeID,
			Stake:     strconv.FormatUint(d.stake, 10),
			StartTime: time.Unix(d.start, 0).UTC(),
			EndTime:   time.Unix(d.end, 0).UTC(),
			Active:    d.end > now,
		})
	}

	total := sum.total
	if active != nil {
		total = uint64(sum.ActiveCount)
		if !*active {
			total = sum.total - uint64(sum.ActiveCount)
		}
	}

	writeJSON(w, http.StatusOK, DelegatorResponse{
		Data: page,
		Meta: DelegatorMeta{
			Meta:    Meta{Total: int64(total), Limit: limit, Offset: offset, HasMore: uint64(offset+limit) < total},
			Summary: sum.DelegatorSummary,
		},
	})
}

func boolKey(b *bool) string {
	if b == nil {
		return "all"
	}
	return strconv.FormatBool(*b)
}

// delegatorTotals is the summary plus the all-history delegation count.
type delegatorTotals struct {
	DelegatorSummary
	total uint64
}

// Delegation tx columns, shared by the reward-address queries.
const (
	delegationBaseWhere = `p_chain_id = 0
		  AND tx_type IN ('AddDelegator', 'AddPermissionlessDelegator')
		  AND toString(tx_data.subnetID) IN ('', ?)
		  AND position(ifNull(toJSONString(tx_data.rewardsOwner.addresses), ''), ?) > 0`
	delegationStakeExpr = `toUInt64OrZero(toString(tx_data.validator.weight))`
	delegationStartExpr = `toUInt64OrZero(toString(tx_data.validator.start))`
	delegationEndExpr   = `toUInt64OrZero(toString(tx_data.validator.end))`
)

var delegatorSortCols = map[string]string{"": "stake", "stake": "stake", "end_time": "end_ts", "start_time": "start_ts"}

// loadDelegatorSummary computes the all-history count and first delegation, and
// the active count, stake and validators. The quoted CB58 match on the JSON text
// can't hit a substring of another address.
func (s *Server) loadDelegatorSummary(ctx context.Context, short string) (delegatorTotals, error) {
	var out delegatorTotals
	quoted := `"` + short + `"`

	var first uint64
	if err := s.conn.QueryRow(ctx, `
		SELECT uniqExact(tx_id), minIf(st, st > 0)
		FROM (
			SELECT tx_id, `+delegationStartExpr+` AS st
			FROM p_chain_txs
			WHERE `+delegationBaseWhere+`
		)
	`, primaryNetworkSubnetID, quoted).Scan(&out.total, &first); err != nil {
		return out, err
	}
	if out.total > 0 && first > 0 {
		t := time.Unix(int64(first), 0).UTC()
		out.FirstDelegationTime = &t
	}

	// Running delegations started within the last year, so the block_time bound
	// keeps this to recent txs. GROUP BY tx_id dedupes re-inserted txs.
	var activeCount, validators uint64
	if err := s.conn.QueryRow(ctx, `
		SELECT count(), toString(sum(toUInt128(stake))), uniqExact(node)
		FROM (
			SELECT tx_id, any(toString(tx_data.validator.nodeID)) AS node, any(`+delegationStakeExpr+`) AS stake
			FROM p_chain_txs
			WHERE `+delegationBaseWhere+`
			  AND block_time >= now() - INTERVAL 400 DAY
			  AND `+delegationEndExpr+` > ?
			GROUP BY tx_id
		)
	`, primaryNetworkSubnetID, quoted, time.Now().Unix()).Scan(&activeCount, &out.ActiveStake, &validators); err != nil {
		return out, err
	}
	out.ActiveCount, out.ValidatorCount = int(activeCount), int(validators)
	return out, nil
}

// loadDelegatorPage returns one sorted page. LIMIT 1 BY tx_id drops re-inserted
// duplicates, which sort next to each other because their columns are identical.
func (s *Server) loadDelegatorPage(ctx context.Context, short string, active *bool, sortKey, order string, limit, offset int) ([]delegationRow, error) {
	dir := "DESC"
	if order == "asc" {
		dir = "ASC"
	}
	where := delegationBaseWhere
	args := []interface{}{primaryNetworkSubnetID, `"` + short + `"`}
	if active != nil {
		if *active {
			where += ` AND block_time >= now() - INTERVAL 400 DAY AND ` + delegationEndExpr + ` > ?`
		} else {
			where += ` AND ` + delegationEndExpr + ` <= ?`
		}
		args = append(args, time.Now().Unix())
	}
	args = append(args, limit, offset)

	rows, err := s.conn.Query(ctx, `
		SELECT tx_id, node, stake, start_ts, end_ts
		FROM (
			SELECT tx_id, toString(tx_data.validator.nodeID) AS node,
				`+delegationStakeExpr+` AS stake, `+delegationStartExpr+` AS start_ts, `+delegationEndExpr+` AS end_ts
			FROM p_chain_txs
			WHERE `+where+`
		)
		ORDER BY `+delegatorSortCols[sortKey]+` `+dir+`, tx_id
		LIMIT 1 BY tx_id
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []delegationRow{}
	for rows.Next() {
		var d delegationRow
		var start, end uint64
		if err := rows.Scan(&d.txID, &d.nodeID, &d.stake, &start, &end); err != nil {
			return nil, err
		}
		d.start, d.end = int64(start), int64(end)
		out = append(out, d)
	}
	return out, rows.Err()
}

// delegationParams parses active, sort and order, writing a 400 on bad input.
func delegationParams(w http.ResponseWriter, q url.Values) (*bool, func(a, b delegationRow) bool, bool) {
	var active *bool
	if a := q.Get("active"); a != "" {
		b, err := strconv.ParseBool(a)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "active must be true or false")
			return nil, nil, false
		}
		active = &b
	}
	less, ok := delegationLess(q.Get("sort"), q.Get("order"))
	if !ok {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "sort must be stake, end_time or start_time and order desc or asc")
		return nil, nil, false
	}
	return active, less, true
}

// filterSortDelegations returns a sorted copy of rows matching the active filter.
func filterSortDelegations(all []delegationRow, active *bool, less func(a, b delegationRow) bool, now int64) []delegationRow {
	var rows []delegationRow
	for _, d := range all {
		if active == nil || (d.end > now) == *active {
			rows = append(rows, d)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return less(rows[i], rows[j]) })
	return rows
}

func pageOf(rows []delegationRow, limit, offset int) []delegationRow {
	if offset >= len(rows) {
		return nil
	}
	return rows[offset:min(offset+limit, len(rows))]
}

// delegationLess returns the row ordering for a sort key and direction, with
// tx_id as the tiebreaker so pages are stable.
func delegationLess(sortKey, order string) (func(a, b delegationRow) bool, bool) {
	var key func(d delegationRow) int64
	switch sortKey {
	case "", "stake":
		key = func(d delegationRow) int64 { return int64(d.stake) }
	case "end_time":
		key = func(d delegationRow) int64 { return d.end }
	case "start_time":
		key = func(d delegationRow) int64 { return d.start }
	default:
		return nil, false
	}
	desc := true
	switch strings.ToLower(order) {
	case "", "desc":
	case "asc":
		desc = false
	default:
		return nil, false
	}
	return func(a, b delegationRow) bool {
		ka, kb := key(a), key(b)
		if ka != kb {
			return (ka > kb) == desc
		}
		return a.txID < b.txID
	}, true
}

// loadDelegations reads Primary Network delegation txs matching cond, which
// takes one bound argument.
func (s *Server) loadDelegations(ctx context.Context, cond string, arg interface{}, onlyRunning bool) ([]delegationRow, error) {
	// GROUP BY tx_id dedupes re-inserted txs more cheaply than FINAL.
	query := `
		SELECT
			tx_id,
			any(toString(tx_data.validator.nodeID)),
			any(ifNull(toJSONString(tx_data.rewardsOwner.addresses), '')),
			any(toUInt64OrZero(toString(tx_data.validator.weight))),
			any(toUInt64OrZero(toString(tx_data.validator.start))),
			any(toUInt64OrZero(toString(tx_data.validator.end)))
		FROM p_chain_txs
		WHERE p_chain_id = 0
		  AND tx_type IN ('AddDelegator', 'AddPermissionlessDelegator')
		  AND ` + cond + `
		  AND toString(tx_data.subnetID) IN ('', ?)`
	if onlyRunning {
		query += `
		  AND block_time >= now() - INTERVAL 400 DAY`
	}
	query += `
		GROUP BY tx_id`

	rows, err := s.conn.Query(ctx, query, arg, primaryNetworkSubnetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []delegationRow{}
	for rows.Next() {
		var d delegationRow
		var owners string
		var start, end uint64
		if err := rows.Scan(&d.txID, &d.nodeID, &owners, &d.stake, &start, &end); err != nil {
			return nil, err
		}
		d.start, d.end = int64(start), int64(end)
		d.owners = parseRewardOwners(owners)
		out = append(out, d)
	}
	return out, rows.Err()
}

// parseRewardOwners decodes the JSON address array from tx_data and converts
// each CB58 address to P-avax1 form. Returns nil when there are none.
func parseRewardOwners(raw string) []string {
	var addrs []*string
	if raw == "" || json.Unmarshal([]byte(raw), &addrs) != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		if a != nil && *a != "" {
			out = append(out, formatBalanceOwner(*a))
		}
	}
	return out
}
