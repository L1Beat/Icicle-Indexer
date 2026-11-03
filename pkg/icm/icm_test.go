package icm

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/sha3"
)

func TestTopics(t *testing.T) {
	topic := func(sig string) string {
		h := sha3.NewLegacyKeccak256()
		h.Write([]byte(sig))
		return hex.EncodeToString(h.Sum(nil))
	}
	const msg = "(uint256,address,bytes32,address,uint256,address[],(uint256,address)[],bytes)"
	require.Equal(t, topicSend, topic("SendCrossChainMessage(bytes32,bytes32,"+msg+",(address,uint256))"))
	require.Equal(t, topicReceive, topic("ReceiveCrossChainMessage(bytes32,bytes32,address,address,"+msg+")"))
}

func hexAddr(t *testing.T, s string) [20]byte {
	var a [20]byte
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	copy(a[:], b)
	return a
}

// TestDecodeMessage decodes real C-Chain messenger logs. The two USDC amounts
// match the USDC Transfer logs in the same transactions.
func TestDecodeMessage(t *testing.T) {
	raw, err := os.ReadFile("testdata/teleporter_logs.json")
	require.NoError(t, err)
	var logs []struct {
		TransactionHash string
		Topics          []string
		Data            string
	}
	require.NoError(t, json.Unmarshal(raw, &logs))
	require.Len(t, logs, 4)
	decode := func(i int) Message {
		l := logs[i]
		data, err := hex.DecodeString(l.Data[2:])
		require.NoError(t, err)
		var t3 []byte
		if len(l.Topics) > 3 {
			t3, err = hex.DecodeString(l.Topics[3][2:])
			require.NoError(t, err)
		}
		m, err := decodeMessage(l.Topics[0] == "0x"+topicSend, data, t3)
		require.NoError(t, err, l.TransactionHash)
		return m
	}
	usdcHome := hexAddr(t, "4ef4752d1a4a6bb1fa7bc862ba646643c7f30ed3")

	// A USDC TokenHome (ICTT v1) sending 12 USDC.
	m := decode(0)
	require.Equal(t, usdcHome, m.Local)
	require.Equal(t, hexAddr(t, "6db0954c10bbede03f3e1a4fa32794728a888a78"), m.Remote)
	require.Equal(t, uint64(300000), m.RequiredGasLimit)
	require.Equal(t, int8(ICTTSingleHopSend), m.ICTTType)
	require.Equal(t, hexAddr(t, "792557f7e4313c684ea0e25b36f7bac2da898cf2"), m.ICTTRecipient)
	require.Equal(t, "12000000", m.ICTTAmount.String())
	require.Equal(t, [20]byte{}, m.Deliverer)

	// The same home receiving 190 USDC back.
	m = decode(1)
	require.Equal(t, usdcHome, m.Local)
	require.Equal(t, hexAddr(t, "0af5068561ab6cbb9af1c74a23881a9d363d3049"), m.Deliverer)
	require.Equal(t, hexAddr(t, "0af5068561ab6cbb9af1c74a23881a9d363d3049"), m.RewardRedeemer)
	require.Equal(t, hexAddr(t, "d30f76ce73272e834157164285be58b336e738ac"), m.ICTTRecipient)
	require.Equal(t, "190000000", m.ICTTAmount.String())

	// A non-ICTT app paying a relayer fee.
	m = decode(2)
	require.Equal(t, hexAddr(t, "a8baad3115a133b101ef935cb2e198fd04f1c659"), m.Local)
	require.Equal(t, hexAddr(t, "0200000000000000000000000000000000000001"), m.FeeToken)
	require.Equal(t, int8(ICTTNone), m.ICTTType)

	// One of the Omnicoin burst's 10.04 XOM transfers to the XOM TokenRemote.
	m = decode(3)
	require.Equal(t, hexAddr(t, "af72f6caf76775daeabaf5c82ce05d97359c8fa1"), m.Local)
	require.Equal(t, hexAddr(t, "c0fb51a663aded93983e107c962edac040e27b2d"), m.Remote)
	require.Equal(t, hexAddr(t, "c59029fb6fa509624a958734c4b2f3d006922bcb"), m.ICTTRecipient)
	require.Equal(t, "10039886999999999996", m.ICTTAmount.String())

	// Truncated data is an error, not a panic.
	data, _ := hex.DecodeString(logs[0].Data[2:])
	for _, n := range []int{0, 31, 64, 200, len(data) - 1} {
		_, err := decodeMessage(true, data[:n], nil)
		require.Error(t, err, n)
	}
}

func TestDecodeICTTShape(t *testing.T) {
	w := func(v uint64) []byte { b := make([]byte, 32); new(big.Int).SetUint64(v).FillBytes(b); return b }
	msg := func(typ uint64, payload ...[]byte) []byte {
		p := bytes.Join(payload, nil)
		return bytes.Join([][]byte{w(32), w(typ), w(64), w(uint64(len(p))), p}, nil)
	}
	decode := func(b []byte) Message {
		m := Message{ICTTType: ICTTNone, ICTTAmount: new(big.Int)}
		decodeICTT(&m, b)
		return m
	}
	m := decode(msg(ICTTSingleHopSend, w(0xf1), w(5)))
	require.Equal(t, int8(ICTTSingleHopSend), m.ICTTType)
	require.Equal(t, "5", m.ICTTAmount.String())
	// Wrong payload length, unknown type, wrong head offset, trailing bytes.
	require.Equal(t, int8(ICTTNone), decode(msg(ICTTSingleHopSend, w(0xf1))).ICTTType)
	require.Equal(t, int8(ICTTNone), decode(msg(9, w(0xf1), w(5))).ICTTType)
	bad := msg(ICTTSingleHopSend, w(0xf1), w(5))
	copy(bad, w(64))
	require.Equal(t, int8(ICTTNone), decode(bad).ICTTType)
	require.Equal(t, int8(ICTTNone), decode(append(msg(ICTTSingleHopSend, w(0xf1), w(5)), w(0)...)).ICTTType)
	// A multi-hop send names its recipient in word 2 and amount in word 3.
	m = decode(msg(ICTTMultiHopSend, w(1), w(2), w(0xf2), w(7), w(0), w(0), w(0)))
	require.Equal(t, int8(ICTTMultiHopSend), m.ICTTType)
	require.Equal(t, byte(0xf2), m.ICTTRecipient[19])
	require.Equal(t, "7", m.ICTTAmount.String())
}

type fakeCaller map[string][]byte // "address fn" -> result, missing = revert

func (f fakeCaller) Call(ctx context.Context, to string, data []byte) ([]byte, error) {
	for k, v := range f {
		var addr, fn string
		fmt.Sscan(k, &addr, &fn)
		h := sha3.NewLegacyKeccak256()
		h.Write([]byte(fn))
		if "0x"+addr == to && bytes.Equal(h.Sum(nil)[:4], data) {
			if v == nil {
				return nil, errors.New("dial tcp: connection refused")
			}
			return v, nil
		}
	}
	return nil, errors.New("rpc error: execution reverted")
}

func TestCheckApp(t *testing.T) {
	word := func(h string) []byte { b, _ := hex.DecodeString(strings.Repeat("0", 64-len(h)) + h); return b }
	cchain := hex.EncodeToString(cChainBlockchainID)
	str := func(s string) []byte {
		return append(append(word("20"), word(fmt.Sprintf("%x", len(s)))...), append([]byte(s), make([]byte, 32-len(s))...)...)
	}
	const home, remote, other, usdc = "00000000000000000000000000000000000000a1", "00000000000000000000000000000000000000a2", "00000000000000000000000000000000000000a3", "b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"
	c := fakeCaller{
		// ICTT v1 home: public variables.
		home + " blockchainID()": word(cchain),
		home + " tokenAddress()": word(usdc),
		usdc + " symbol()":       str("USDC"),
		usdc + " decimals()":     word("6"),
		// Current remote: getters, and the remote is the token.
		remote + " getBlockchainID()":     word(cchain),
		remote + " getTokenHomeAddress()": word("c0fb51a663aded93983e107c962edac040e27b2d"),
		remote + " symbol()":              str("XOM"),
		remote + " decimals()":            word("12"),
		// Not the C-Chain.
		other + " getBlockchainID()": word("01"),
	}
	app, err := checkApp(context.Background(), c, home)
	require.NoError(t, err)
	require.Equal(t, App{Address: home, Kind: KindICTTHome, Token: usdc, Symbol: "USDC", Decimals: 6, Note: "blockchainID(), tokenAddress()"}, app)
	app, err = checkApp(context.Background(), c, remote)
	require.NoError(t, err)
	require.Equal(t, KindICTTRemote, app.Kind)
	require.Equal(t, remote, app.Token)
	require.Equal(t, "XOM", app.Symbol)
	require.Equal(t, uint8(18), app.Decimals)
	app, err = checkApp(context.Background(), c, other)
	require.NoError(t, err)
	require.Equal(t, "", app.Kind)
	// A transport failure is returned, so the window is retried.
	_, err = checkApp(context.Background(), fakeCaller{other + " getBlockchainID()": nil}, other)
	require.Error(t, err)
}
