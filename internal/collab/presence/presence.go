package presence

import (
	"encoding/json"
	"sort"

	"github.com/reearth/ygo/awareness"

	"github.com/misty-org/misty-server/internal/collab/wire"
)

const (
	IdentityField = "misty"
	maxStateBytes = 64 * 1024
	maxRoomBytes  = 4 << 20
	maxClients    = 1024
)

type Owner struct {
	Conn   string
	Person string
}

type Presence struct {
	states *awareness.Awareness
	owners map[uint64]Owner
}

func New() *Presence {
	states := awareness.New(0)
	states.SetMaxBytes(maxRoomBytes)
	states.SetMaxClients(maxClients)
	return &Presence{states: states, owners: map[uint64]Owner{}}
}

func OwnedBySomeoneElse(owners map[uint64]Owner, sender Owner, clients []uint64) bool {
	for _, client := range clients {
		held, ok := owners[client]
		if !ok || held.Conn == sender.Conn || held.Person == sender.Person {
			continue
		}
		return true
	}
	return false
}

func withIdentity(state []byte, identity wire.Identity) ([]byte, bool) {
	fields := map[string]json.RawMessage{}
	if json.Unmarshal(state, &fields) != nil {
		return nil, false
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return nil, false
	}
	fields[IdentityField] = raw
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, false
	}
	return out, true
}

func (p *Presence) Apply(sender Owner, identity wire.Identity, update []byte) ([]byte, error) {
	entries, err := wire.DecodeAwareness(update)
	if err != nil {
		return nil, err
	}
	clients := make([]uint64, 0, len(entries))
	kept := make([]wire.AwarenessEntry, 0, len(entries))
	for _, entry := range entries {
		clients = append(clients, entry.Client)
		if !entry.Removed() {
			if len(entry.State) > maxStateBytes {
				continue
			}
			state, ok := withIdentity(entry.State, identity)
			if !ok {
				continue
			}
			entry.State = state
		}
		kept = append(kept, entry)
	}
	if OwnedBySomeoneElse(p.owners, sender, clients) || len(kept) == 0 {
		return nil, nil
	}
	result, err := p.states.ApplyUpdateWith(wire.EncodeAwareness(kept), sender.Conn, awareness.ApplyOptions{})
	if err != nil {
		return nil, wire.ErrBadFrame
	}
	applied := dedupe(result.Applied)
	if len(applied) == 0 {
		return nil, nil
	}
	live := p.states.GetStates()
	for _, client := range applied {
		if _, alive := live[client]; alive {
			p.owners[client] = sender
		} else {
			delete(p.owners, client)
		}
	}
	return p.states.EncodeUpdate(applied), nil
}

func (p *Presence) remove(clients []uint64) []byte {
	live := p.states.GetStates()
	entries := make([]wire.AwarenessEntry, 0, len(clients))
	for _, client := range clients {
		delete(p.owners, client)
		state, alive := live[client]
		if !alive {
			continue
		}
		entries = append(entries, wire.AwarenessEntry{Client: client, Clock: state.Clock + 1, State: []byte("null")})
	}
	if len(entries) == 0 {
		return nil
	}
	update := wire.EncodeAwareness(entries)
	if _, err := p.states.ApplyUpdateWith(update, nil, awareness.ApplyOptions{}); err != nil {
		return nil
	}
	return update
}

func (p *Presence) Leave(conn string) []byte {
	clients := []uint64{}
	for client, owner := range p.owners {
		if owner.Conn == conn {
			clients = append(clients, client)
		}
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i] < clients[j] })
	return p.remove(clients)
}

func (p *Presence) Owned(conn string) []uint64 {
	clients := []uint64{}
	for client, owner := range p.owners {
		if owner.Conn == conn {
			clients = append(clients, client)
		}
	}
	return clients
}

func (p *Presence) All() []byte {
	live := p.states.GetStates()
	if len(live) == 0 {
		return nil
	}
	clients := make([]uint64, 0, len(live))
	for client := range live {
		clients = append(clients, client)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i] < clients[j] })
	return p.states.EncodeUpdate(clients)
}

func (p *Presence) Stop() {
	p.states.Destroy()
}

func dedupe(clients []uint64) []uint64 {
	seen := map[uint64]bool{}
	out := make([]uint64, 0, len(clients))
	for _, client := range clients {
		if !seen[client] {
			seen[client] = true
			out = append(out, client)
		}
	}
	return out
}
