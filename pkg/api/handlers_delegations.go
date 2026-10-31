package api

import (
	"context"
	"encoding/json"
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
	delegationsCache = newTTLCache(5 * time.Minute)
	delegatorCache   = newTTLCache(5 * time.Minute)
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
// @Description Primary Network AddDelegator / AddPermissionlessDelegator transactions whose reward owner includes this address, across all validators. meta.summary covers all of the address's delegations regardless of the active filter. Stake is a nAVAX string. Cached for 5 minutes.
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
	active, less, ok := delegationParams(w, r.URL.Query())
	if !ok {
		return
	}
	limit, offset := getPaginationWithCap(r, 500)

	// The summary needs the full history, so there is one cache entry per address.
	// The quoted CB58 match on the JSON text can't hit a substring of another address.
	v, err := delegatorCache.get(r.Context(), short, func(ctx context.Context) (interface{}, error) {
		return s.loadDelegations(ctx, "position(ifNull(toJSONString(tx_data.rewardsOwner.addresses), ''), ?) > 0", `"`+short+`"`, false)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}

	now := time.Now().Unix()
	all := v.([]delegationRow)
	rows := filterSortDelegations(all, active, less, now)
	page := []DelegatorDelegation{}
	for _, d := range pageOf(rows, limit, offset) {
		page = append(page, DelegatorDelegation{
			TxID:      d.txID,
			NodeID:    d.nodeID,
			Stake:     strconv.FormatUint(d.stake, 10),
			StartTime: time.Unix(d.start, 0).UTC(),
			EndTime:   time.Unix(d.end, 0).UTC(),
			Active:    d.end > now,
		})
	}

	writeJSON(w, http.StatusOK, DelegatorResponse{
		Data: page,
		Meta: DelegatorMeta{
			Meta:    Meta{Total: int64(len(rows)), Limit: limit, Offset: offset, HasMore: offset+limit < len(rows)},
			Summary: summarizeDelegations(all, now),
		},
	})
}

func summarizeDelegations(rows []delegationRow, now int64) DelegatorSummary {
	var out DelegatorSummary
	var stake uint64
	nodes := map[string]bool{}
	var first int64
	for _, d := range rows {
		if first == 0 || d.start < first {
			first = d.start
		}
		if d.end > now {
			out.ActiveCount++
			stake += d.stake
			nodes[d.nodeID] = true
		}
	}
	out.ActiveStake = strconv.FormatUint(stake, 10)
	out.ValidatorCount = len(nodes)
	if first > 0 {
		t := time.Unix(first, 0).UTC()
		out.FirstDelegationTime = &t
	}
	return out
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
