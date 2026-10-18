package agent

import "testing"

func TestDecideMatrix(t *testing.T) {
	cases := []struct {
		mode    string
		class   ActionClass
		covered bool
		want    Decision
	}{
		{PermissionAsk, ClassRead, false, DecisionAllow},
		{PermissionAuto, ClassRead, false, DecisionAllow},
		{PermissionAsk, ClassScopeImplied, false, DecisionPause},
		{PermissionAsk, ClassScopeImplied, true, DecisionPause},
		{PermissionAuto, ClassScopeImplied, false, DecisionAllow},
		// Ask is forceful: a remembered grant never skips its pause.
		{PermissionAsk, ClassPermission, true, DecisionPause},
		{PermissionAsk, ClassPermission, false, DecisionPause},
		{PermissionAuto, ClassPermission, true, DecisionAllow},
		{PermissionAuto, ClassPermission, false, DecisionPause},
		{PermissionAsk, ClassNeverRemember, true, DecisionPause},
		{PermissionAuto, ClassNeverRemember, true, DecisionPause},
		// The retired full mode is Auto; an unknown mode is treated as Ask.
		{ModeFull, ClassPermission, true, DecisionAllow},
		{"", ClassPermission, true, DecisionPause},
		{"bogus", ClassScopeImplied, false, DecisionPause},
	}
	for _, tc := range cases {
		if got := Decide(tc.mode, tc.class, tc.covered); got != tc.want {
			t.Errorf("Decide(%q, %q, %v) = %q, want %q", tc.mode, tc.class, tc.covered, got, tc.want)
		}
	}
}

func TestGrantScopes(t *testing.T) {
	if !ValidGrantScope(GrantGitPush, ScopeRepo, "misty-org/misty") {
		t.Fatal("a concrete repo is a valid push scope")
	}
	for _, id := range []string{"", "*", "misty-org/*", " ", "a?b"} {
		if ValidGrantScope(GrantGitPush, ScopeRepo, id) {
			t.Fatalf("scope %q must be rejected", id)
		}
	}
	if ValidGrantScope(GrantGitPush, ScopeOrigin, "https://github.com") {
		t.Fatal("push grants name a repo, not an origin")
	}
	for _, action := range []GrantAction{ActionScreenControl, ActionAccountSetting, ActionOutsideProject, ActionToolWrite, "made.up"} {
		if Rememberable(action) {
			t.Fatalf("%s must not be rememberable", action)
		}
	}
	if ClassOf("made.up") != ClassNeverRemember {
		t.Fatal("unknown actions always pause")
	}
}

func TestRecallGrantsAreScopedToMemoryAndForgettingAlwaysAsks(t *testing.T) {
	if !Rememberable(GrantRecallRead) || ClassOf(GrantRecallRead) != ClassPermission {
		t.Fatal("searching Memory should be a rememberable permission")
	}
	if !ValidGrantScope(GrantRecallRead, ScopeRecall, RecallScopeID) || ValidGrantScope(GrantRecallRead, ScopeSpace, "space-1") || ValidGrantScope(GrantRecallRead, ScopeRecall, "*") {
		t.Fatal("a Memory grant names Memory and nothing else")
	}
	if Rememberable(ActionRecallForget) || ClassOf(ActionRecallForget) != ClassNeverRemember {
		t.Fatal("forgetting must never be remembered")
	}
	for _, mode := range []string{PermissionAsk, PermissionAuto} {
		if Decide(mode, ClassOf(ActionRecallForget), true) != DecisionPause {
			t.Fatalf("forgetting continued without asking in %s", mode)
		}
		if Decide(mode, ClassOf(GrantRecallRead), false) != DecisionPause {
			t.Fatalf("searching Memory continued without permission in %s", mode)
		}
	}
	if Decide(PermissionAuto, ClassOf(GrantRecallRead), true) != DecisionAllow || Decide(PermissionAsk, ClassOf(GrantRecallRead), true) != DecisionPause {
		t.Fatal("a remembered Memory grant applies in Auto only")
	}
}
