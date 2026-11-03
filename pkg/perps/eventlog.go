package perps

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math/big"
)

// eventLog is a decoded GMX V2 EventEmitter event: its name and the scalar
// key/value items of its EventLogData. Array items and bytes/string values are
// not needed here and are skipped.
type eventLog struct {
	Name  string
	Addr  map[string]string   // lowercase hex, no 0x
	Uint  map[string]*big.Int // uintItems
	Int   map[string]*big.Int // intItems
	Bool  map[string]bool
	Bytes map[string][32]byte // bytes32Items
}

func (e eventLog) uint(k string) *big.Int {
	if v, ok := e.Uint[k]; ok {
		return v
	}
	return new(big.Int)
}

var errShort = errors.New("short event data")

// abiReader reads ABI-encoded data with bounds checks.
type abiReader []byte

func (r abiReader) word(off int) ([]byte, error) {
	if off < 0 || off+32 > len(r) {
		return nil, errShort
	}
	return r[off : off+32], nil
}

// int reads a word as a non-negative offset or length that fits in the data.
func (r abiReader) int(off int) (int, error) {
	w, err := r.word(off)
	if err != nil {
		return 0, err
	}
	for _, b := range w[:24] {
		if b != 0 {
			return 0, errShort
		}
	}
	n := binary.BigEndian.Uint64(w[24:])
	if n > uint64(len(r)) {
		return 0, errShort
	}
	return int(n), nil
}

func (r abiReader) str(off int) (string, error) {
	n, err := r.int(off)
	if err != nil {
		return "", err
	}
	if off+32+n > len(r) {
		return "", errShort
	}
	return string(r[off+32 : off+32+n]), nil
}

// decodeEventLog decodes the data of EventLog, EventLog1 and EventLog2, which
// share it: (address msgSender, string eventName, EventLogData eventData).
// EventLogData is seven groups (address, uint, int, bool, bytes32, bytes,
// string), each a tuple of (KeyValue[] items, KeyArrayValue[] arrayItems).
func decodeEventLog(data []byte) (eventLog, error) {
	r := abiReader(data)
	e := eventLog{Addr: map[string]string{}, Uint: map[string]*big.Int{}, Int: map[string]*big.Int{},
		Bool: map[string]bool{}, Bytes: map[string][32]byte{}}
	nameOff, err := r.int(32)
	if err != nil {
		return e, err
	}
	if e.Name, err = r.str(nameOff); err != nil {
		return e, err
	}
	base, err := r.int(64)
	if err != nil {
		return e, err
	}
	for g := 0; g < 5; g++ { // bytes and string groups are skipped
		goff, err := r.int(base + 32*g)
		if err != nil {
			return e, err
		}
		gbase := base + goff
		ioff, err := r.int(gbase)
		if err != nil {
			return e, err
		}
		items := gbase + ioff
		n, err := r.int(items)
		if err != nil {
			return e, err
		}
		for i := 0; i < n; i++ {
			koff, err := r.int(items + 32 + 32*i)
			if err != nil {
				return e, err
			}
			kv := items + 32 + koff
			keyOff, err := r.int(kv)
			if err != nil {
				return e, err
			}
			key, err := r.str(kv + keyOff)
			if err != nil {
				return e, err
			}
			v, err := r.word(kv + 32)
			if err != nil {
				return e, err
			}
			switch g {
			case 0:
				e.Addr[key] = hex.EncodeToString(v[12:])
			case 1:
				e.Uint[key] = new(big.Int).SetBytes(v)
			case 2:
				x := new(big.Int).SetBytes(v)
				if v[0]&0x80 != 0 {
					x.Sub(x, new(big.Int).Lsh(big.NewInt(1), 256))
				}
				e.Int[key] = x
			case 3:
				e.Bool[key] = v[31] != 0
			case 4:
				var b [32]byte
				copy(b[:], v)
				e.Bytes[key] = b
			}
		}
	}
	return e, nil
}
