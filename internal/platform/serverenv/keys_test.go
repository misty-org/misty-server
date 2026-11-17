package serverenv

import (
	"strings"
	"testing"
)

func testRoot() []byte {
	root := make([]byte, 32)
	for i := range root {
		root[i] = byte(i)
	}
	return root
}

// The derivation is part of every server's data: a changed label or info
// string would sign everyone out and lose encrypted data. These are fixed.
func TestDerivationIsStable(t *testing.T) {
	root := testRoot()
	api, err := APISecret(root)
	if err != nil || api != "ngoFzJLPWqOXWTsdWxEBPEWwpB9WlZcURKYxXcMc9ec=" {
		t.Fatalf("API secret %s %v", api, err)
	}
	auth, _ := Lookup("MISTY_AUTH_SIGNING_KEY")
	source, _ := auth.Source(root)
	if v1, _ := auth.Value(source, 1); v1 != "nH6SgdDyP9gZDGDx4btc4KJOWoEDvwCXNxnr48f20e8=" {
		t.Fatalf("auth v1 %s", v1)
	}
	if v2, _ := auth.Value(source, 2); v2 != "BGrVmSAJeI9QWaka+iqGUI+2boCqd2RZZL8mm/Ik1vI=" {
		t.Fatalf("auth v2 %s", v2)
	}
}

func TestEveryKeyIsDistinctAndWellFormed(t *testing.T) {
	root := testRoot()
	seen := map[string]string{}
	purposes := map[string]bool{}
	for _, key := range Keys {
		if purposes[key.Purpose] {
			t.Fatalf("purpose %s used twice", key.Purpose)
		}
		purposes[key.Purpose] = true
		source, err := key.Source(root)
		if err != nil {
			t.Fatal(err)
		}
		value, err := key.Value(source, 1)
		if err != nil || value == "" {
			t.Fatalf("%s: %v", key.Name, err)
		}
		if other, ok := seen[value]; ok {
			t.Fatalf("%s and %s derive the same value", key.Name, other)
		}
		seen[value] = key.Name
		if key.Format == Ed25519 {
			public, err := key.PreviousValue(source, 1)
			if err != nil || public == value || len(public) != 44 {
				t.Fatalf("the previous slot of %s holds its public key: %q %v", key.Name, public, err)
			}
		}
	}
	if _, err := Encode(Hex, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
}

func TestVersionsRoundTrip(t *testing.T) {
	versions := Versions{"auth-signing": {Current: 3, Previous: 2}, "metrics-token": {Current: 2}, "operator-token": {Current: 1}}
	text := versions.String()
	if text != "auth-signing=3/2,metrics-token=2" {
		t.Fatalf("versions %q", text)
	}
	parsed, err := ParseVersions(text)
	if err != nil || parsed.Of("auth-signing") != (Version{3, 2}) || parsed.Of("operator-token") != (Version{Current: 1}) {
		t.Fatalf("parsed %v %v", parsed, err)
	}
	for _, bad := range []string{"auth-signing", "auth-signing=0", "auth-signing=2/2", "=2", "auth-signing=x"} {
		if _, err := ParseVersions(bad); err == nil {
			t.Fatalf("%q must not parse", bad)
		}
	}
}

func TestDefaultsFillOnlyWhatIsUnset(t *testing.T) {
	root := testRoot()
	api, _ := APISecret(root)
	env := map[string]string{
		APISecretName:              api,
		VersionsName:               "auth-signing=2/1",
		"MISTY_HOSTNAME":           "Cloud.Misty.Example",
		"MISTY_ENVIRONMENT":        "production",
		"MISTY_OPERATOR_TOKEN":     "misty_op_stored",
		"R2_BUCKET":                "my-bucket",
		"DB_MIGRATION_PASSWORD":    "",
		"JOURNAL_COLLAB_ROOM_SALT": "",
	}
	defaults, err := Defaults(func(name string) string { return env[name] })
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := defaults["MISTY_OPERATOR_TOKEN"]; ok {
		t.Fatal("a stored value must win")
	}
	if _, ok := defaults["R2_BUCKET"]; ok {
		t.Fatal("a bucket the operator set must win")
	}
	if defaults["MISTY_PUBLIC_API_URL"] != "https://cloud.misty.example/v1" {
		t.Fatalf("public URL %q", defaults["MISTY_PUBLIC_API_URL"])
	}
	for _, name := range []string{"DB_MIGRATION_PASSWORD", "DB_MIGRATION_PASSWORD_PREVIOUS", "MISTY_BACKUP_AGE_IDENTITY", RootName} {
		if defaults[name] != "" {
			t.Fatalf("the API must not derive %s", name)
		}
	}
	auth, _ := Lookup("MISTY_AUTH_SIGNING_KEY")
	source, _ := auth.Source(root)
	v1, _ := auth.Value(source, 1)
	v2, _ := auth.Value(source, 2)
	if defaults["MISTY_AUTH_SIGNING_KEY"] != v2 || defaults["MISTY_AUTH_SIGNING_KEY_PREVIOUS"] != v1 {
		t.Fatal("versions were not applied")
	}
	if defaults["JOURNAL_COLLAB_ROOM_SALT"] == "" || defaults["DB_PASSWORD"] == "" {
		t.Fatal("unset keys must be derived")
	}
	// Outside production nothing follows from the hostname, and without the
	// API secret no key is derived: a development stack keeps its own.
	env["MISTY_ENVIRONMENT"], env[APISecretName] = "development", ""
	if defaults, _ := Defaults(func(name string) string { return env[name] }); len(defaults) != 0 {
		t.Fatalf("development got %v", defaults)
	}
	env[APISecretName] = "short"
	if _, err := Defaults(func(name string) string { return env[name] }); err == nil || !strings.Contains(err.Error(), APISecretName) {
		t.Fatalf("a damaged API secret must fail: %v", err)
	}
}
