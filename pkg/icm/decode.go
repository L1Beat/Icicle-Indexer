package icm

import (
	"errors"
	"math"
	"math/big"
)

// ICTT message types (TransferrerMessageType in ava-labs/icm-contracts
// ictt/interfaces/ITokenTransferrer.sol). -1 is a message that does not decode
// as an ICTT TransferrerMessage.
const (
	ICTTNone           = -1
	ICTTRegisterRemote = 0
	ICTTSingleHopSend  = 1
	ICTTSingleHopCall  = 2
	ICTTMultiHopSend   = 3
	ICTTMultiHopCall   = 4
)

// ICTTTypeNames are the API names of the ICTT message types.
var ICTTTypeNames = map[int8]string{
	ICTTRegisterRemote: "register_remote",
	ICTTSingleHopSend:  "single_hop_send",
	ICTTSingleHopCall:  "single_hop_call",
	ICTTMultiHopSend:   "multi_hop_send",
	ICTTMultiHopCall:   "multi_hop_call",
}

// Message is the decoded part of a SendCrossChainMessage or
// ReceiveCrossChainMessage log that the API serves. Addresses are 20 bytes.
type Message struct {
	Local            [20]byte // C-Chain side: originSenderAddress for sends, destinationAddress for receives
	Remote           [20]byte // L1 side: destinationAddress for sends, originSenderAddress for receives
	RequiredGasLimit uint64
	FeeToken         [20]byte // sends only
	FeeAmount        *big.Int // sends only
	Deliverer        [20]byte // receives only
	RewardRedeemer   [20]byte // receives only
	ICTTType         int8
	ICTTRecipient    [20]byte // recipient, or recipientContract for calls
	ICTTAmount       *big.Int
}

var errShort = errors.New("short data")

func word(b []byte, off uint64) ([]byte, error) {
	if off > uint64(len(b)) || uint64(len(b))-off < 32 {
		return nil, errShort
	}
	return b[off : off+32], nil
}

// offset reads a word that must fit in a uint32 (an ABI offset or length).
func offset(b []byte, off uint64) (uint64, error) {
	w, err := word(b, off)
	if err != nil {
		return 0, err
	}
	n := new(big.Int).SetBytes(w)
	if n.BitLen() > 32 {
		return 0, errors.New("offset out of range")
	}
	return n.Uint64(), nil
}

func addrAt(b []byte, off uint64) ([20]byte, error) {
	var a [20]byte
	w, err := word(b, off)
	if err != nil {
		return a, err
	}
	copy(a[:], w[12:])
	return a, nil
}

func uintAt(b []byte, off uint64) (*big.Int, error) {
	w, err := word(b, off)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(w), nil
}

// decodeMessage decodes a messenger log's data. deliverer is topic3 of a
// receive (the 32-byte topic), nil for a send.
//
// SendCrossChainMessage data: (TeleporterMessage message, (address,uint256)
// feeInfo), so word 0 is the message offset and words 1-2 the fee.
// ReceiveCrossChainMessage data: (address rewardRedeemer, TeleporterMessage
// message). TeleporterMessage is (messageNonce, originSenderAddress,
// destinationBlockchainID, destinationAddress, requiredGasLimit,
// allowedRelayerAddresses, receipts, message).
func decodeMessage(sent bool, data []byte, deliverer []byte) (Message, error) {
	m := Message{ICTTType: ICTTNone, FeeAmount: new(big.Int), ICTTAmount: new(big.Int)}
	var base uint64
	var err error
	if sent {
		if base, err = offset(data, 0); err != nil {
			return m, err
		}
		if m.FeeToken, err = addrAt(data, 32); err != nil {
			return m, err
		}
		if m.FeeAmount, err = uintAt(data, 64); err != nil {
			return m, err
		}
	} else {
		if m.RewardRedeemer, err = addrAt(data, 0); err != nil {
			return m, err
		}
		if base, err = offset(data, 32); err != nil {
			return m, err
		}
		if len(deliverer) == 32 {
			copy(m.Deliverer[:], deliverer[12:])
		}
	}
	origin, err := addrAt(data, base+32)
	if err != nil {
		return m, err
	}
	dest, err := addrAt(data, base+96)
	if err != nil {
		return m, err
	}
	if sent {
		m.Local, m.Remote = origin, dest
	} else {
		m.Local, m.Remote = dest, origin
	}
	gas, err := uintAt(data, base+128)
	if err != nil {
		return m, err
	}
	if gas.IsUint64() {
		m.RequiredGasLimit = gas.Uint64()
	} else {
		m.RequiredGasLimit = math.MaxUint64
	}
	msgOff, err := offset(data, base+224)
	if err != nil {
		return m, err
	}
	n, err := offset(data, base+msgOff)
	if err != nil {
		return m, err
	}
	start := base + msgOff + 32
	if start > uint64(len(data)) || uint64(len(data))-start < n {
		return m, errShort
	}
	decodeICTT(&m, data[start:start+n])
	return m, nil
}

// decodeICTT fills the ICTT fields when payload is an abi-encoded
// TransferrerMessage (uint8 messageType, bytes payload) of the exact shape
// TokenHome and TokenRemote send. Anything else leaves ICTTType at ICTTNone.
// The shape alone does not prove the sender is ICTT; that is checked on-chain.
func decodeICTT(m *Message, b []byte) {
	if len(b) < 128 {
		return
	}
	head, _ := offset(b, 0)
	typ, err1 := offset(b, 32)
	inner, _ := offset(b, 64)
	n, err2 := offset(b, 96)
	if err1 != nil || err2 != nil || head != 32 || inner != 64 || typ > ICTTMultiHopCall {
		return
	}
	if uint64(len(b)) != 128+(n+31)/32*32 {
		return
	}
	p := b[128 : 128+n]
	// Word positions of the recipient and amount in each payload struct, and
	// its fixed length (0 when it has a dynamic field).
	var rcpt, amt, size uint64
	switch typ {
	case ICTTRegisterRemote: // (initialReserveImbalance, remoteTokenDecimals, homeTokenDecimals)
		if n != 96 {
			return
		}
		m.ICTTType = int8(typ)
		return
	case ICTTSingleHopSend: // (recipient, amount)
		rcpt, amt, size = 0, 1, 64
	case ICTTSingleHopCall: // (sourceBlockchainID, originTokenTransferrerAddress, originSenderAddress, recipientContract, amount, ...)
		rcpt, amt = 3, 4
	case ICTTMultiHopSend: // (destinationBlockchainID, destinationTokenTransferrerAddress, recipient, amount, ...)
		rcpt, amt, size = 2, 3, 224
	case ICTTMultiHopCall: // (originSenderAddress, destinationBlockchainID, destinationTokenTransferrerAddress, recipientContract, amount, ...)
		rcpt, amt = 3, 4
	}
	if size != 0 && n != size {
		return
	}
	r, err := addrAt(p, rcpt*32)
	if err != nil {
		return
	}
	a, err := uintAt(p, amt*32)
	if err != nil {
		return
	}
	m.ICTTType, m.ICTTRecipient, m.ICTTAmount = int8(typ), r, a
}
