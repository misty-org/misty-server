package db

import "testing"

func TestServerOwnerComesOnlyFromConfig(t *testing.T) {
	t.Setenv("MISTY_OWNER_EMAIL", "")
	if IsServerOwnerAccount("first@example.com", "google") {
		t.Fatal("a server without MISTY_OWNER_EMAIL has an owner")
	}

	t.Setenv("MISTY_OWNER_EMAIL", " Owner@Example.com ")
	cases := []struct {
		email, provider string
		owner           bool
	}{
		{"owner@example.com", "google", true},
		{"  OWNER@example.COM", "google", true},
		{"owner@example.com", "misty", false},
		{"first@example.com", "google", false},
		{"", "google", false},
	}
	for _, c := range cases {
		if got := IsServerOwnerAccount(c.email, c.provider); got != c.owner {
			t.Errorf("IsServerOwnerAccount(%q, %q) = %v, want %v", c.email, c.provider, got, c.owner)
		}
	}
	if !IsServerOwnerEmail("owner@EXAMPLE.com") || IsServerOwnerEmail("other@example.com") {
		t.Fatal("IsServerOwnerEmail does not match MISTY_OWNER_EMAIL")
	}
}
