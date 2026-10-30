package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildStakingSummary(t *testing.T) {
	// 4 validators, total stake 100 (own 60 + delegated 40).
	vals := []stakeRow{
		{own: 30, delegated: 20, delegations: 5, fee: 2},
		{own: 10, delegated: 10, delegations: 3, fee: 4.5},
		{own: 15, delegated: 5, delegations: 1, fee: 10},
		{own: 5, delegated: 5, delegations: 2, fee: 100},
	}
	s := buildStakingSummary(vals, 400, time.Unix(0, 0))

	require.Equal(t, uint32(4), s.ValidatorCount)
	require.Equal(t, uint64(11), s.DelegationCount)
	require.Equal(t, "60", s.OwnStake)
	require.Equal(t, "40", s.DelegatedStake)
	require.Equal(t, "100", s.TotalStake)
	require.Equal(t, "400", *s.TotalSupply)
	require.InDelta(t, 25.0, *s.StakedSupplyPercent, 1e-9)

	// Median of 2, 4.5, 10, 100.
	require.InDelta(t, 7.25, s.DelegationFee.MedianPercent, 1e-9)
	counts := []uint32{}
	for _, b := range s.DelegationFee.Histogram {
		counts = append(counts, b.ValidatorCount)
	}
	require.Equal(t, []uint32{1, 1, 1, 0, 1}, counts) // 2 | 2-5 | 5-10 | 10-20 | 20+
	require.Equal(t, "2%", s.DelegationFee.Histogram[0].Label)
	require.Equal(t, "20%+", s.DelegationFee.Histogram[4].Label)
	require.Nil(t, s.DelegationFee.Histogram[4].MaxPercent)
	require.Equal(t, "50", s.DelegationFee.Histogram[0].TotalStake)

	// Sorted totals 50, 20, 20, 10: top validator alone holds 50% (not > 50%).
	require.Equal(t, uint32(1), s.Concentration.Nakamoto33)
	require.Equal(t, uint32(2), s.Concentration.Nakamoto50)
	require.Equal(t, 100.0, s.Concentration.Top10Percent)
}

func TestBuildStakingSummary_NoSupply(t *testing.T) {
	s := buildStakingSummary(nil, 0, time.Time{})
	require.Nil(t, s.TotalSupply)
	require.Nil(t, s.StakedSupplyPercent)
	require.Equal(t, "0", s.TotalStake)
}

func TestValidatorSortClause(t *testing.T) {
	c, ok := validatorSortClause("", "")
	require.True(t, ok)
	require.Equal(t, "v.weight DESC, v.validation_id", c)

	c, ok = validatorSortClause("total_stake", "asc")
	require.True(t, ok)
	require.Equal(t, "v.weight + v.delegated_stake ASC, v.validation_id", c)

	_, ok = validatorSortClause("weight; DROP TABLE x", "")
	require.False(t, ok)
	_, ok = validatorSortClause("weight", "sideways")
	require.False(t, ok)
}

func TestHandleListValidators_InvalidSort(t *testing.T) {
	server := NewTestServer(&MockConn{})
	w := MakeRequest(t, server, "GET", "/api/v1/data/validators?sort=bogus")
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStakingModel(t *testing.T) {
	require.Equal(t, "pos", stakingModel(primaryNetworkSubnetID, "", "", false))
	require.Equal(t, "poa", stakingModel("s", "PoA", "Proof of Stake", true)) // on-chain type wins
	require.Equal(t, "pos", stakingModel("s", "PoS-erc20", "", false))
	require.Equal(t, "poa", stakingModel("s", "unknown", "Proof of Authority", false))
	require.Equal(t, "pos", stakingModel("s", "", "", true))
	require.Equal(t, "unknown", stakingModel("s", "", "", false))
}

func TestHandleStakingUnlocks_InvalidWeeks(t *testing.T) {
	server := NewTestServer(&MockConn{})
	for _, q := range []string{"0", "54", "x"} {
		w := MakeRequest(t, server, "GET", "/api/v1/metrics/staking/unlocks?weeks="+q)
		require.Equal(t, http.StatusBadRequest, w.Code, q)
	}
}
