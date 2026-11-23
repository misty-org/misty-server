package api

import "testing"

func TestLeftoversStayWhileAnotherConnectionOfTheSameAccountHoldsThem(t *testing.T) {
	s := NewRealtimeService(nil, "")
	gone := testRealtimeClient(s, "a", 4)
	live := testRealtimeClient(s, "a", 4)
	s.Mu.Lock()
	live.typingScopes = map[string]bool{"space\x00": true}
	live.agentStates = map[string]string{"space\x00agent": "working\x00editing"}
	s.Mu.Unlock()
	s.Mu.Lock()
	delete(s.clients, gone)
	delete(s.clientsByUser["a"], gone)
	s.Mu.Unlock()

	s.clearLeftovers(gone, []string{"space\x00"}, map[string]bool{"space": true})

	s.Mu.RLock()
	defer s.Mu.RUnlock()
	if gone.typingScopes != nil {
		t.Fatal("cleared typing that another connection of the account still holds")
	}
	if !gone.lastAgentsAt.IsZero() || gone.agentStates != nil {
		t.Fatal("cleared agents that another connection of the account still holds")
	}
	if !s.otherClientHoldsLocked(gone, func(other *RealtimeClient) bool { return other == live }) {
		t.Fatal("the live connection was not found")
	}
}
