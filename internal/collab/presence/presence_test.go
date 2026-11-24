package presence

import (
	"encoding/json"
	"testing"

	"github.com/misty-org/misty-server/internal/collab/wire"
)

func update(entries ...wire.AwarenessEntry) []byte { return wire.EncodeAwareness(entries) }

func state(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestReadsTheClientIDsAnUpdateCarries(t *testing.T) {
	entries, err := wire.DecodeAwareness(update(
		wire.AwarenessEntry{Client: 7, Clock: 1, State: []byte(`{"cursor":1}`)},
		wire.AwarenessEntry{Client: 9, Clock: 2, State: []byte(`null`)},
	))
	if err != nil || len(entries) != 2 || entries[0].Client != 7 || entries[1].Client != 9 || !entries[1].Removed() {
		t.Fatalf("entries %v %v", entries, err)
	}
	if _, err := wire.DecodeAwareness([]byte{1, 0, 1}); err == nil {
		t.Fatal("a truncated update was read")
	}
	if _, err := wire.DecodeAwareness(update(wire.AwarenessEntry{Client: 0, Clock: 1, State: []byte(`{}`)})); err != wire.ErrClientZero {
		t.Fatalf("client 0 was not refused: %v", err)
	}
}

func TestMovesAReSentStateToTheConnectionThatSentIt(t *testing.T) {
	p := New()
	defer p.Stop()
	ana := wire.Identity{Kind: "person", ID: "ana"}
	if relay, err := p.Apply(Owner{Conn: "old", Person: "person:ana"}, ana, update(wire.AwarenessEntry{Client: 7, Clock: 1, State: []byte(`{}`)})); err != nil || relay == nil {
		t.Fatalf("first state %v", err)
	}
	if relay, err := p.Apply(Owner{Conn: "fresh", Person: "person:ana"}, ana, update(wire.AwarenessEntry{Client: 7, Clock: 2, State: []byte(`{}`)})); err != nil || relay == nil {
		t.Fatalf("same person again %v", err)
	}
	if owned := p.Owned("fresh"); len(owned) != 1 || owned[0] != 7 {
		t.Fatalf("fresh owns %v", owned)
	}
	if owned := p.Owned("old"); len(owned) != 0 {
		t.Fatalf("old still owns %v", owned)
	}
}

func TestDropsRemovedStatesFromTheSender(t *testing.T) {
	p := New()
	defer p.Stop()
	ana := wire.Identity{Kind: "person", ID: "ana"}
	owner := Owner{Conn: "sender", Person: "person:ana"}
	_, _ = p.Apply(owner, ana, update(wire.AwarenessEntry{Client: 7, Clock: 1, State: []byte(`{}`)}, wire.AwarenessEntry{Client: 8, Clock: 1, State: []byte(`{}`)}))
	_, _ = p.Apply(owner, ana, update(wire.AwarenessEntry{Client: 7, Clock: 2, State: []byte(`null`)}))
	if owned := p.Owned("sender"); len(owned) != 1 || owned[0] != 8 {
		t.Fatalf("sender owns %v", owned)
	}
}

func TestRefusesAStateAnotherPersonOwnsButNotTheSamePersons(t *testing.T) {
	owners := map[uint64]Owner{7: {Conn: "owner", Person: "person:ana"}}
	if !OwnedBySomeoneElse(owners, Owner{Conn: "intruder", Person: "person:sam"}, []uint64{7}) {
		t.Fatal("another person's state was taken")
	}
	if OwnedBySomeoneElse(owners, Owner{Conn: "again", Person: "person:ana"}, []uint64{7}) {
		t.Fatal("the same person was refused")
	}
	if OwnedBySomeoneElse(owners, Owner{Conn: "intruder", Person: "person:sam"}, []uint64{8}) {
		t.Fatal("a free client id was refused")
	}
	p := New()
	defer p.Stop()
	_, _ = p.Apply(Owner{Conn: "owner", Person: "person:ana"}, wire.Identity{Kind: "person", ID: "ana"}, update(wire.AwarenessEntry{Client: 7, Clock: 1, State: []byte(`{}`)}))
	if relay, _ := p.Apply(Owner{Conn: "intruder", Person: "person:sam"}, wire.Identity{Kind: "person", ID: "sam"}, update(wire.AwarenessEntry{Client: 7, Clock: 5, State: []byte(`{}`)})); relay != nil {
		t.Fatal("an intruder's state was relayed")
	}
}

func TestTheIdentityGuardCorrectsTheMistyField(t *testing.T) {
	p := New()
	defer p.Stop()
	ana := wire.Identity{Kind: "person", ID: "ana", Name: "Ana"}
	relay, err := p.Apply(Owner{Conn: "c", Person: "person:ana"}, ana, update(wire.AwarenessEntry{Client: 42, Clock: 1,
		State: state(t, map[string]any{"cursor": 3, "misty": map[string]any{"kind": "person", "id": "sam"}})}))
	if err != nil || relay == nil {
		t.Fatalf("relay %v", err)
	}
	entries, err := wire.DecodeAwareness(relay)
	if err != nil || len(entries) != 1 {
		t.Fatalf("relay entries %v %v", entries, err)
	}
	var got struct {
		Cursor int           `json:"cursor"`
		Misty  wire.Identity `json:"misty"`
	}
	if err := json.Unmarshal(entries[0].State, &got); err != nil {
		t.Fatal(err)
	}
	if got.Misty.ID != "ana" || got.Misty.Name != "Ana" || got.Cursor != 3 {
		t.Fatalf("state %+v", got)
	}
}

func TestLeaveRemovesTheConnectionsStates(t *testing.T) {
	p := New()
	defer p.Stop()
	ana := wire.Identity{Kind: "person", ID: "ana"}
	_, _ = p.Apply(Owner{Conn: "c", Person: "person:ana"}, ana, update(wire.AwarenessEntry{Client: 3, Clock: 4, State: []byte(`{}`)}))
	removal := p.Leave("c")
	entries, err := wire.DecodeAwareness(removal)
	if err != nil || len(entries) != 1 || entries[0].Client != 3 || entries[0].Clock != 5 || !entries[0].Removed() {
		t.Fatalf("removal %v %v", entries, err)
	}
	if p.All() != nil {
		t.Fatal("a removed state is still listed")
	}
}
