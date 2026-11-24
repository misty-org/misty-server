package wire

import (
	"errors"

	"github.com/reearth/ygo/encoding"
)

var ErrClientZero = errors.New("wire: client id 0 is not allowed")

const maxAwarenessEntries = 4096

type AwarenessEntry struct {
	Client uint64
	Clock  uint64
	State  []byte
}

func (e AwarenessEntry) Removed() bool {
	return len(e.State) == 0 || string(e.State) == "null"
}

func DecodeAwareness(update []byte) ([]AwarenessEntry, error) {
	dec := encoding.NewDecoder(update)
	count, err := dec.ReadVarUint()
	if err != nil || count > maxAwarenessEntries {
		return nil, ErrBadFrame
	}
	entries := make([]AwarenessEntry, 0, count)
	for i := uint64(0); i < count; i++ {
		client, err := dec.ReadVarUint()
		if err != nil {
			return nil, ErrBadFrame
		}
		clock, err := dec.ReadVarUint()
		if err != nil {
			return nil, ErrBadFrame
		}
		state, err := dec.ReadVarBytes()
		if err != nil {
			return nil, ErrBadFrame
		}
		if client == 0 {
			return nil, ErrClientZero
		}
		entries = append(entries, AwarenessEntry{Client: client, Clock: clock, State: state})
	}
	return entries, nil
}

func EncodeAwareness(entries []AwarenessEntry) []byte {
	enc := encoding.NewEncoder()
	enc.WriteVarUint(uint64(len(entries)))
	for _, entry := range entries {
		enc.WriteVarUint(entry.Client)
		enc.WriteVarUint(entry.Clock)
		state := entry.State
		if len(state) == 0 {
			state = []byte("null")
		}
		enc.WriteVarBytes(state)
	}
	return enc.Bytes()
}
