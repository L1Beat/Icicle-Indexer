package lending

import (
	"encoding/hex"
	"fmt"
	"math/big"
)

// Multicall3 aggregate3 support. We always use aggregate3 with allowFailure=true
// so a single reverting account (edge cases on getUserAccountData or a per-market
// read) does not fail the whole batch (rule 5).

// aggregate3((address,bool,bytes)[]) returns ((bool,bytes)[])
var aggregate3Selector = Selector("aggregate3((address,bool,bytes)[])")

// Call is one sub-call inside an aggregate3 batch.
type Call struct {
	Target       string // 0x address
	AllowFailure bool
	Data         string // calldata, hex with or without 0x
}

// CallResult is one decoded aggregate3 result.
type CallResult struct {
	Success    bool
	ReturnData []byte
}

// EncodeAggregate3 builds the calldata for a Multicall3 aggregate3 call.
func EncodeAggregate3(calls []Call) string {
	// Encode each tuple (address target, bool allowFailure, bytes callData).
	elems := make([][]byte, len(calls))
	for i, c := range calls {
		data := decodeHex(c.Data)
		var e []byte
		e = append(e, leftPadAddress(c.Target)...)               // target
		e = append(e, boolWord(c.AllowFailure)...)               // allowFailure
		e = append(e, u256Word(big.NewInt(0x60))...)             // offset to bytes within tuple
		e = append(e, u256Word(big.NewInt(int64(len(data))))...) // bytes length
		e = append(e, rightPad32(data)...)                       // bytes payload
		elems[i] = e
	}

	// Dynamic array of dynamic tuples: head is N offset words (relative to the
	// start of the head region, just after the length word), tails follow.
	n := len(calls)
	headLen := 32 * n
	var heads, tails []byte
	off := headLen
	for _, e := range elems {
		heads = append(heads, u256Word(big.NewInt(int64(off)))...)
		tails = append(tails, e...)
		off += len(e)
	}

	var arr []byte
	arr = append(arr, u256Word(big.NewInt(int64(n)))...)
	arr = append(arr, heads...)
	arr = append(arr, tails...)

	var out []byte
	out = append(out, u256Word(big.NewInt(0x20))...) // offset to the array
	out = append(out, arr...)

	return aggregate3Selector + hex.EncodeToString(out)
}

// DecodeAggregate3 decodes the return data of an aggregate3 call. The input is
// the raw eth_call result (no function selector).
func DecodeAggregate3(out []byte) ([]CallResult, error) {
	if len(out) < 64 {
		return nil, fmt.Errorf("aggregate3 result too short: %d bytes", len(out))
	}
	arrBase := int(wordU64(out, 0)) // offset to the Result[] array, expected 0x20
	if arrBase < 0 || arrBase+32 > len(out) {
		return nil, fmt.Errorf("aggregate3 array offset out of range: %d", arrBase)
	}

	n := int(new(big.Int).SetBytes(out[arrBase : arrBase+32]).Uint64())
	headBase := arrBase + 32
	results := make([]CallResult, 0, n)

	for i := 0; i < n; i++ {
		hp := headBase + i*32
		if hp+32 > len(out) {
			return nil, fmt.Errorf("aggregate3 head %d out of range", i)
		}
		elemOff := int(new(big.Int).SetBytes(out[hp : hp+32]).Uint64())
		elemStart := headBase + elemOff
		if elemStart+64 > len(out) {
			return nil, fmt.Errorf("aggregate3 element %d out of range", i)
		}
		success := new(big.Int).SetBytes(out[elemStart:elemStart+32]).Sign() != 0
		bytesOff := int(new(big.Int).SetBytes(out[elemStart+32 : elemStart+64]).Uint64())
		bytesStart := elemStart + bytesOff
		if bytesStart+32 > len(out) {
			return nil, fmt.Errorf("aggregate3 element %d bytes header out of range", i)
		}
		blen := int(new(big.Int).SetBytes(out[bytesStart : bytesStart+32]).Uint64())
		dataStart := bytesStart + 32
		if dataStart+blen > len(out) {
			return nil, fmt.Errorf("aggregate3 element %d payload out of range", i)
		}
		data := make([]byte, blen)
		copy(data, out[dataStart:dataStart+blen])
		results = append(results, CallResult{Success: success, ReturnData: data})
	}
	return results, nil
}
