package perps

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// testdata/gmx_v2_events.json holds real C-Chain EventEmitter logs (Oct-Nov
// 2026), with the values eth_abi decoded from them.
func TestDecodeEventLog_RealLogs(t *testing.T) {
	raw, err := os.ReadFile("testdata/gmx_v2_events.json")
	require.NoError(t, err)
	var fixtures map[string]struct {
		Data   string                 `json:"data"`
		Expect map[string]interface{} `json:"expect"`
	}
	require.NoError(t, json.Unmarshal(raw, &fixtures))
	require.Len(t, fixtures, 6)
	for name, f := range fixtures {
		b, err := hex.DecodeString(strings.TrimPrefix(f.Data, "0x"))
		require.NoError(t, err)
		e, err := decodeEventLog(b)
		require.NoError(t, err, name)
		if name == "Liquidation" {
			require.Equal(t, "PositionDecrease", e.Name)
		} else {
			require.Equal(t, name, e.Name)
		}
		for k, want := range f.Expect {
			switch w := want.(type) {
			case bool:
				require.Equal(t, w, e.Bool[k], name+" "+k)
			case string:
				if strings.HasPrefix(w, "0x") {
					require.Equal(t, strings.TrimPrefix(w, "0x"), e.Addr[k], name+" "+k)
				} else {
					got, ok := e.Uint[k]
					if !ok {
						got, ok = e.Int[k] // e.g. collateralDeltaAmount is an int item on increases
					}
					require.True(t, ok, name+" "+k)
					require.Equal(t, w, got.String(), name+" "+k)
				}
			}
		}
	}
}

func TestDecodeEventLog_Garbage(t *testing.T) {
	_, err := decodeEventLog(make([]byte, 10))
	require.Error(t, err)
	b := make([]byte, 96)
	b[63] = 0xff // name offset past the end
	_, err = decodeEventLog(b)
	require.Error(t, err)
}

func TestTopics(t *testing.T) {
	// Prefixes seen on-chain on the EventEmitter and the V1 Vault.
	require.True(t, strings.HasPrefix(topicV2EventLog, "7e3bde2b"))
	require.True(t, strings.HasPrefix(topicV2EventLog1, "137a4406"))
	require.True(t, strings.HasPrefix(topicV2EventLog2, "468a25a7"))
	require.True(t, strings.HasPrefix(topicV1Increase, "2fe68525"))
	require.True(t, strings.HasPrefix(topicV1Decrease, "93d75d64"))
}

func TestMarketNames(t *testing.T) {
	// Synthetic index tokens seen in MarketCreated on the C-Chain.
	require.Equal(t, "c301e6fe31062c557aee806cc6a841ae989a3ac6", syntheticAddress(43114, "DOGE"))
	require.Equal(t, "bdf8a77acb7a54597e7760b34d3e632912bb59b7", syntheticAddress(43114, "WLFI"))
	n, p := MarketName("34b2885d617ce2dded4f60ccb49809fc17bb58af", "b31f66aa3c1e785363f0875a1b74e27b85fd66c7", "b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e")
	require.Equal(t, "XRP/USD", n)
	require.Equal(t, "AVAX-USDC", p)
	n, p = MarketName("152b9d0fdc40c096757f570a51e494bd4b943e50", "", "")
	require.Equal(t, "BTC/USD", n)
	require.Equal(t, "", p)
	n, _ = MarketName(strings.Repeat("0", 40), "b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e", "c7198437980c041c805a1edcba50c1ce5db95118")
	require.Equal(t, "Swap only", n)
}
