package api

import "testing"

func TestAskContextSpaceRequiresUnambiguousOrigin(t *testing.T) {
	for _, tc := range []struct {
		name string
		refs []aiContextReference
		want string
	}{
		{name: "unbound"},
		{name: "one Space", refs: []aiContextReference{{SpaceID: "a"}}, want: "a"},
		{name: "same Space references", refs: []aiContextReference{{SpaceID: "a"}, {SpaceID: " a "}}, want: "a"},
		{name: "mixed Spaces", refs: []aiContextReference{{SpaceID: "a"}, {SpaceID: "b"}}},
		{name: "unscoped source", refs: []aiContextReference{{}, {SpaceID: "a"}}, want: "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstAIContextSpace(tc.refs); got != tc.want {
				t.Fatalf("Space=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestAskRejectsMixedContextBeforeAdmission(t *testing.T) {
	body := aiInvocationInput{Mode: "drawer", SurfaceID: "global", Trigger: "message", Prompt: "Create a task", Context: []aiContextReference{{SpaceID: "a"}, {SpaceID: "b"}}}
	if validateAIInvocationInput(&body) == nil {
		t.Fatal("mixed originating Spaces admitted")
	}
}
