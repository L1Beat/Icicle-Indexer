package api

import (
	"context"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Staking endpoints for the Primary Network. Validator and delegation numbers
// come from l1_validator_state, which the validator syncer refreshes from
// platform.getCurrentValidators every 5 minutes. Amounts are nAVAX as decimal
// strings (they exceed JS safe integers).

// StakingSummary is the Primary Network staking overview.
type StakingSummary struct {
	AsOf                time.Time          `json:"as_of"` // last validator sync
	ValidatorCount      uint32             `json:"validator_count"`
	DelegationCount     uint64             `json:"delegation_count"` // active delegations, not unique delegators
	OwnStake            string             `json:"own_stake"`
	DelegatedStake      string             `json:"delegated_stake"`
	TotalStake          string             `json:"total_stake"`
	TotalSupply         *string            `json:"total_supply"`          // AVAX total supply (not circulating), null if not yet sampled
	StakedSupplyPercent *float64           `json:"staked_supply_percent"` // total_stake / total_supply * 100
	DelegationFee       DelegationFees     `json:"delegation_fee"`
	Concentration       StakeConcentration `json:"concentration"`
}

// DelegationFees summarizes validator delegation fees.
type DelegationFees struct {
	MedianPercent float64     `json:"median_percent"`
	Histogram     []FeeBucket `json:"histogram"`
}

// FeeBucket counts validators whose fee is in (min_percent, max_percent].
// The first bucket is exactly the 2% protocol minimum, the last is open-ended
// (max_percent null).
type FeeBucket struct {
	Label          string   `json:"label"`
	MinPercent     float64  `json:"min_percent"`
	MaxPercent     *float64 `json:"max_percent"`
	ValidatorCount uint32   `json:"validator_count"`
	TotalStake     string   `json:"total_stake"`
}

// StakeConcentration measures how concentrated total stake (own + delegated)
// is across validators.
type StakeConcentration struct {
	Top10Percent  float64 `json:"top10_percent"`
	Top50Percent  float64 `json:"top50_percent"`
	Top100Percent float64 `json:"top100_percent"`
	Nakamoto33    uint32  `json:"nakamoto_33"` // fewest validators holding > 33% of total stake
	Nakamoto50    uint32  `json:"nakamoto_50"` // fewest validators holding > 50% of total stake
}

// StakingUnlockWeek is the stake whose lock ends during one week.
type StakingUnlockWeek struct {
	WeekStart       time.Time `json:"week_start"` // Monday 00:00 UTC
	ValidatorStake  string    `json:"validator_stake"`
	DelegatedStake  string    `json:"delegated_stake"`
	ValidatorCount  uint32    `json:"validator_count"`
	DelegationCount uint32    `json:"delegation_count"`
}

// StakingPoint is one daily staking snapshot.
type StakingPoint struct {
	Date            string  `json:"date"`
	ValidatorCount  uint32  `json:"validator_count"`
	DelegationCount uint32  `json:"delegation_count"`
	OwnStake        string  `json:"own_stake"`
	DelegatedStake  string  `json:"delegated_stake"`
	TotalStake      string  `json:"total_stake"`
	TotalSupply     *string `json:"total_supply,omitempty"`  // Primary Network only
	StakedTokens    *string `json:"staked_tokens,omitempty"` // total_stake in whole tokens (PoS only)
	StakedToken     string  `json:"staked_token,omitempty"`
}

// ttlCache memoizes expensive, rarely changing responses per key.
type ttlCache struct {
	ttl     time.Duration
	mu      sync.Mutex
	entries map[string]ttlEntry
}

type ttlEntry struct {
	value   interface{}
	fetched time.Time
}

func newTTLCache(ttl time.Duration) *ttlCache {
	return &ttlCache{ttl: ttl, entries: map[string]ttlEntry{}}
}

// get returns the cached value for key or loads it. Errors are not cached.
// Concurrent misses may load twice, which is fine at these request rates.
func (c *ttlCache) get(ctx context.Context, key string, load func(context.Context) (interface{}, error)) (interface{}, error) {
	c.mu.Lock()
	e, ok := c.entries[key]
	c.mu.Unlock()
	if ok && time.Since(e.fetched) < c.ttl {
		return e.value, nil
	}
	v, err := load(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.entries[key] = ttlEntry{value: v, fetched: time.Now()}
	c.mu.Unlock()
	return v, nil
}

var (
	stakingSummaryCache = newTTLCache(time.Minute)
	stakingUnlockCache  = newTTLCache(5 * time.Minute)
)

// handleStakingSummary returns the Primary Network staking overview
// @Summary Primary Network staking summary
// @Description Own and delegated stake, validator/delegation counts, delegation fee distribution, stake concentration (top N share, Nakamoto) and share of AVAX total supply staked. Amounts are nAVAX strings. Cached for 1 minute, source refreshes every 5.
// @Tags Metrics - Staking
// @Produce json
// @Success 200 {object} Response{data=StakingSummary}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/staking [get]
func (s *Server) handleStakingSummary(w http.ResponseWriter, r *http.Request) {
	v, err := stakingSummaryCache.get(r.Context(), "primary", s.loadStakingSummary)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

type stakeRow struct {
	own, delegated uint64
	delegations    uint32
	fee            float64
}

func (s *Server) loadStakingSummary(ctx context.Context) (interface{}, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT weight, delegated_stake, delegator_count, delegation_fee_percent, last_updated
		FROM (SELECT * FROM l1_validator_state FINAL)
		WHERE p_chain_id = 0 AND subnet_id = ? AND active = true
	`, primaryNetworkSubnetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vals []stakeRow
	var asOf time.Time
	for rows.Next() {
		var sr stakeRow
		var updated time.Time
		if err := rows.Scan(&sr.own, &sr.delegated, &sr.delegations, &sr.fee, &updated); err != nil {
			return nil, err
		}
		if updated.After(asOf) {
			asOf = updated
		}
		vals = append(vals, sr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var supply uint64
	_ = s.conn.QueryRow(ctx, `
		SELECT total_supply
		FROM staking_snapshots FINAL
		WHERE p_chain_id = 0 AND subnet_id = ? AND total_supply > 0
		ORDER BY snapshot_date DESC
		LIMIT 1
	`, primaryNetworkSubnetID).Scan(&supply)

	return buildStakingSummary(vals, supply, asOf), nil
}

// feeBucketBounds are the upper bounds of the fee histogram buckets; the
// first is the 2% protocol minimum, the last bucket is open-ended.
var feeBucketBounds = []float64{2, 5, 10, 20}

func buildStakingSummary(vals []stakeRow, supply uint64, asOf time.Time) StakingSummary {
	out := StakingSummary{AsOf: asOf, ValidatorCount: uint32(len(vals))}

	own, delegated := new(big.Int), new(big.Int)
	totals := make([]uint64, len(vals))
	fees := make([]float64, len(vals))
	buckets := make([]FeeBucket, len(feeBucketBounds)+1)
	bucketStake := make([]*big.Int, len(buckets))
	for i := range buckets {
		bucketStake[i] = new(big.Int)
		if i == 0 {
			buckets[i] = FeeBucket{Label: "2%", MinPercent: 2, MaxPercent: &feeBucketBounds[0]}
			continue
		}
		lo := feeBucketBounds[i-1]
		buckets[i].MinPercent = lo
		if i < len(feeBucketBounds) {
			hi := feeBucketBounds[i]
			buckets[i].MaxPercent = &hi
			buckets[i].Label = strconv.FormatFloat(lo, 'f', -1, 64) + "-" + strconv.FormatFloat(hi, 'f', -1, 64) + "%"
		} else {
			buckets[i].Label = strconv.FormatFloat(lo, 'f', -1, 64) + "%+"
		}
	}

	for i, v := range vals {
		own.Add(own, new(big.Int).SetUint64(v.own))
		delegated.Add(delegated, new(big.Int).SetUint64(v.delegated))
		out.DelegationCount += uint64(v.delegations)
		totals[i] = v.own + v.delegated
		fees[i] = v.fee

		b := len(feeBucketBounds)
		for j, hi := range feeBucketBounds {
			if v.fee <= hi {
				b = j
				break
			}
		}
		buckets[b].ValidatorCount++
		bucketStake[b].Add(bucketStake[b], new(big.Int).SetUint64(totals[i]))
	}
	for i := range buckets {
		buckets[i].TotalStake = bucketStake[i].String()
	}

	total := new(big.Int).Add(own, delegated)
	out.OwnStake = own.String()
	out.DelegatedStake = delegated.String()
	out.TotalStake = total.String()
	out.DelegationFee.Histogram = buckets

	if len(fees) > 0 {
		sort.Float64s(fees)
		m := len(fees) / 2
		if len(fees)%2 == 1 {
			out.DelegationFee.MedianPercent = fees[m]
		} else {
			out.DelegationFee.MedianPercent = (fees[m-1] + fees[m]) / 2
		}
	}

	if supply > 0 {
		sup := strconv.FormatUint(supply, 10)
		out.TotalSupply = &sup
		pct := bigRatio(total, new(big.Int).SetUint64(supply)) * 100
		out.StakedSupplyPercent = &pct
	}

	// Concentration over total stake, largest first.
	sort.Slice(totals, func(i, j int) bool { return totals[i] > totals[j] })
	totalF := bigFloat(total)
	if totalF > 0 {
		var cum float64
		for i, t := range totals {
			cum += float64(t)
			n := i + 1
			switch n {
			case 10:
				out.Concentration.Top10Percent = cum / totalF * 100
			case 50:
				out.Concentration.Top50Percent = cum / totalF * 100
			case 100:
				out.Concentration.Top100Percent = cum / totalF * 100
			}
			if out.Concentration.Nakamoto33 == 0 && cum > totalF*0.33 {
				out.Concentration.Nakamoto33 = uint32(n)
			}
			if out.Concentration.Nakamoto50 == 0 && cum > totalF*0.50 {
				out.Concentration.Nakamoto50 = uint32(n)
			}
		}
		// Fewer validators than N: the top N hold everything.
		if len(totals) < 10 {
			out.Concentration.Top10Percent = 100
		}
		if len(totals) < 50 {
			out.Concentration.Top50Percent = 100
		}
		if len(totals) < 100 {
			out.Concentration.Top100Percent = 100
		}
	}
	return out
}

func bigFloat(x *big.Int) float64 {
	f, _ := new(big.Float).SetInt(x).Float64()
	return f
}

func bigRatio(a, b *big.Int) float64 {
	if b.Sign() == 0 {
		return 0
	}
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(a), new(big.Float).SetInt(b)).Float64()
	return f
}

// handleStakingUnlocks returns weekly Primary Network stake unlocks
// @Summary Primary Network stake unlock schedule
// @Description Stake whose lock ends in each of the next N weeks (Monday-start, UTC), split into validator own stake and delegated stake. Delegations come from active AddDelegator/AddPermissionlessDelegator transactions. Amounts are nAVAX strings. Cached for 5 minutes.
// @Tags Metrics - Staking
// @Produce json
// @Param weeks query int false "Weeks ahead, 1-53 (the maximum staking period is one year)" default(12)
// @Success 200 {object} Response{data=[]StakingUnlockWeek}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/staking/unlocks [get]
func (s *Server) handleStakingUnlocks(w http.ResponseWriter, r *http.Request) {
	weeks := 12
	if q := r.URL.Query().Get("weeks"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 || n > 53 {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "weeks must be 1-53")
			return
		}
		weeks = n
	}
	v, err := stakingUnlockCache.get(r.Context(), strconv.Itoa(weeks), func(ctx context.Context) (interface{}, error) {
		return s.loadStakingUnlocks(ctx, weeks)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadStakingUnlocks(ctx context.Context, weeks int) ([]StakingUnlockWeek, error) {
	now := time.Now().UTC()
	// Monday of the current week.
	start := now.Truncate(24*time.Hour).AddDate(0, 0, -((int(now.Weekday()) + 6) % 7))
	out := make([]StakingUnlockWeek, weeks)
	index := map[int64]int{}
	for i := range out {
		ws := start.AddDate(0, 0, 7*i)
		out[i] = StakingUnlockWeek{WeekStart: ws, ValidatorStake: "0", DelegatedStake: "0"}
		index[ws.Unix()] = i
	}
	horizon := start.AddDate(0, 0, 7*weeks)

	// Validators: active Primary Network validations ending within the horizon.
	vrows, err := s.conn.Query(ctx, `
		SELECT toStartOfWeek(end_time, 1, 'UTC') AS wk, toUInt32(count()), toString(sum(toUInt128(weight)))
		FROM (SELECT * FROM l1_validator_state FINAL)
		WHERE p_chain_id = 0 AND subnet_id = ? AND active = true
		  AND end_time >= ? AND end_time < ?
		GROUP BY wk
	`, primaryNetworkSubnetID, now, horizon)
	if err != nil {
		return nil, err
	}
	defer vrows.Close()
	for vrows.Next() {
		var wk time.Time
		var cnt uint32
		var stake string
		if err := vrows.Scan(&wk, &cnt, &stake); err != nil {
			return nil, err
		}
		if i, ok := index[time.Date(wk.Year(), wk.Month(), wk.Day(), 0, 0, 0, 0, time.UTC).Unix()]; ok {
			out[i].ValidatorCount, out[i].ValidatorStake = cnt, stake
		}
	}
	if err := vrows.Err(); err != nil {
		return nil, err
	}

	// Delegations: still-running Primary Network delegations ending within
	// the horizon. A delegation lasts at most a year, so only txs from the
	// last ~13 months can still be running (block_time bound prunes the scan).
	// GROUP BY tx_id dedupes re-inserted txs more cheaply than FINAL.
	drows, err := s.conn.Query(ctx, `
		SELECT toStartOfWeek(toDateTime(end_ts, 'UTC'), 1, 'UTC') AS wk, toUInt32(count()), toString(sum(toUInt128(stake)))
		FROM (
			SELECT
				any(toUInt64OrZero(toString(tx_data.validator.end))) AS end_ts,
				any(toUInt64OrZero(toString(tx_data.validator.weight))) AS stake
			FROM p_chain_txs
			WHERE p_chain_id = 0
			  AND tx_type IN ('AddDelegator', 'AddPermissionlessDelegator')
			  AND block_time >= now() - INTERVAL 400 DAY
			  AND toString(tx_data.subnetID) IN ('', ?)
			GROUP BY tx_id
		)
		WHERE end_ts >= ? AND end_ts < ?
		GROUP BY wk
	`, primaryNetworkSubnetID, now.Unix(), horizon.Unix())
	if err != nil {
		return nil, err
	}
	defer drows.Close()
	for drows.Next() {
		var wk time.Time
		var cnt uint32
		var stake string
		if err := drows.Scan(&wk, &cnt, &stake); err != nil {
			return nil, err
		}
		if i, ok := index[time.Date(wk.Year(), wk.Month(), wk.Day(), 0, 0, 0, 0, time.UTC).Unix()]; ok {
			out[i].DelegationCount, out[i].DelegatedStake = cnt, stake
		}
	}
	return out, drows.Err()
}

// handleStakingTimeseries returns daily staking snapshots
// @Summary Daily staking snapshots
// @Description Daily active stake per subnet, sampled by the validator syncer (last sample of each UTC day). Primary Network rows include delegation and AVAX total supply. History starts 2026-10-30. Amounts are raw weight strings (nAVAX for the Primary Network), with staked_tokens for PoS chains. Oldest first.
// @Tags Metrics - Staking
// @Produce json
// @Param subnet_id query string false "Subnet ID (default: Primary Network)"
// @Param days query int false "Days of history, 1-3650" default(365)
// @Success 200 {object} Response{data=[]StakingPoint}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/staking/timeseries [get]
func (s *Server) handleStakingTimeseries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	subnetID := r.URL.Query().Get("subnet_id")
	if subnetID == "" {
		subnetID = primaryNetworkSubnetID
	}
	days := 365
	if q := r.URL.Query().Get("days"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 || n > 3650 {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "days must be 1-3650")
			return
		}
		days = n
	}

	rows, err := s.conn.Query(ctx, `
		SELECT toString(snapshot_date), validator_count, delegation_count, own_stake, delegated_stake, total_supply
		FROM staking_snapshots FINAL
		WHERE p_chain_id = 0 AND subnet_id = ? AND snapshot_date >= today() - ?
		ORDER BY snapshot_date
	`, subnetID, days)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	conv := s.stakeConverters(ctx, []string{subnetID})[subnetID]
	points := []StakingPoint{}
	for rows.Next() {
		var p StakingPoint
		var own, delegated, supply uint64
		if err := rows.Scan(&p.Date, &p.ValidatorCount, &p.DelegationCount, &own, &delegated, &supply); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		total := new(big.Int).Add(new(big.Int).SetUint64(own), new(big.Int).SetUint64(delegated))
		p.OwnStake = strconv.FormatUint(own, 10)
		p.DelegatedStake = strconv.FormatUint(delegated, 10)
		p.TotalStake = total.String()
		if supply > 0 {
			sup := strconv.FormatUint(supply, 10)
			p.TotalSupply = &sup
		}
		if conv.factor != nil {
			t := formatWeightDiv(total, conv.factor)
			p.StakedTokens = &t
			p.StakedToken = conv.token
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: points})
}
