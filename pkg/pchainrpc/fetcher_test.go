package pchainrpc

import (
	"testing"

	"github.com/ava-labs/avalanchego/ids"
)

func TestParseValidatorInfo_Delegation(t *testing.T) {
	primary, _ := ids.FromString("11111111111111111111111111111111LpoYY")
	info := ValidatorInfo{
		TxID:            "uaBoxvk5RunmNYL8xrsyM6CvwAzxmh3F57ciRNFtJsjDVLQsu",
		StartTime:       "1792012560",
		EndTime:         "1793390400",
		Weight:          "1160000000000000",
		NodeID:          "NodeID-Fsuh27EEqD2CuTce4EByoNRE3BMtLzNmB",
		DelegationFee:   "2.0000",
		DelegatorCount:  "1216",
		DelegatorWeight: "1840000000000000",
	}
	st, err := ParseValidatorInfo(info, primary)
	if err != nil {
		t.Fatal(err)
	}
	if st.DelegatorCount != 1216 || st.DelegatedStake != 1840000000000000 || st.DelegationFeePercent != 2 {
		t.Fatalf("got count=%d stake=%d fee=%v", st.DelegatorCount, st.DelegatedStake, st.DelegationFeePercent)
	}

	// L1 validators omit the delegation fields entirely.
	info.DelegationFee, info.DelegatorCount, info.DelegatorWeight = "", "", ""
	st, err = ParseValidatorInfo(info, primary)
	if err != nil {
		t.Fatal(err)
	}
	if st.DelegatorCount != 0 || st.DelegatedStake != 0 || st.DelegationFeePercent != 0 {
		t.Fatalf("expected zero delegation, got %+v", st)
	}
}
