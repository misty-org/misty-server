package capabilities

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func testDocument() InstallDocument {
	return InstallDocument{AppID: "example.habits", Version: "1.0.0", PermissionVersion: 1, Scopes: []string{"capabilities.providers.write", "habits.list"}, Capabilities: Manifest{Protocol: 1, Providers: []Provider{{ID: "example.habits/backend", Version: 1, Label: "Habits", Route: Route{Kind: "backend", ConnectionID: "10000000-0000-4000-8000-000000000001"}, Capabilities: []Definition{{Name: "habits.list", Version: 1, Description: "List recorded habits", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"string"}}`), RequiredScopes: []string{"habits.list"}, Effects: Effects{Kind: "read", Incidental: []string{}, Approval: "none", Retry: "read_only"}}}}}}}
}
func signTest(t *testing.T, document InstallDocument) SignedManifest {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(document)
	return SignedManifest{Document: string(raw), PublicKey: base64.StdEncoding.EncodeToString(pub), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(SignatureDomain+string(raw))))}
}
func TestSignedManifestRejectsChangedContentAndPrivileges(t *testing.T) {
	signed := signTest(t, testDocument())
	if _, err := Verify(signed); err != nil {
		t.Fatal(err)
	}
	signed.Document += " "
	if _, err := Verify(signed); err == nil {
		t.Fatal("signature accepted changed bytes")
	}
	for name, change := range map[string]func(*InstallDocument){
		"host adapter": func(d *InstallDocument) { d.Capabilities.Providers[0].Route = Route{Kind: "server", Adapter: "admin"} },
		"other owner":  func(d *InstallDocument) { d.Capabilities.Providers[0].ID = "another.app/backend" },
		"undeclared grant": func(d *InstallDocument) {
			d.Capabilities.Providers[0].Capabilities[0].RequiredScopes = []string{"notes.write"}
		},
		"unapproved send": func(d *InstallDocument) { d.Capabilities.Providers[0].Capabilities[0].Effects.Kind = "send" },
		"remote schema": func(d *InstallDocument) {
			d.Capabilities.Providers[0].Capabilities[0].InputSchema = json.RawMessage(`{"$ref":"https://example.com/schema"}`)
		},
		"unresolved schema": func(d *InstallDocument) {
			d.Capabilities.Providers[0].Capabilities[0].InputSchema = json.RawMessage(`{"$ref":"#/$defs/missing"}`)
		},
		"prototype schema": func(d *InstallDocument) {
			d.Capabilities.Providers[0].Capabilities[0].InputSchema = json.RawMessage(`{"properties":{"__proto__":{}}}`)
		},
		"route confusion": func(d *InstallDocument) { d.Capabilities.Providers[0].Route.Adapter = "native" },
	} {
		t.Run(name, func(t *testing.T) {
			d := testDocument()
			change(&d)
			if _, err := Verify(signTest(t, d)); err == nil {
				t.Fatal("unsafe manifest accepted")
			}
		})
	}
}
func TestCapabilityDecodeRejectsAmbiguousJSON(t *testing.T) {
	for _, raw := range []string{`{"a":1,"a":2}`, `{"a":{"value":1,"value":2}}`, `{"a":1} {"a":2}`} {
		var out map[string]any
		if Decode([]byte(raw), &out) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	var p Provider
	if Decode([]byte(`{"id":"example.habits/backend","privileged":true}`), &p) == nil {
		t.Fatal("accepted unknown field")
	}
}
func TestCapabilitySchemaValidatesReferencesAndConstraints(t *testing.T) {
	schema, err := CompileSchema(json.RawMessage(`{"type":"object","properties":{"count":{"$ref":"#/$defs/positive"}},"required":["count"],"additionalProperties":false,"$defs":{"positive":{"type":"integer","minimum":1}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(map[string]any{"count": 2}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{map[string]any{"count": 0}, map[string]any{"count": 1, "extra": true}, map[string]any{}} {
		if schema.Validate(value) == nil {
			t.Fatalf("accepted invalid input %#v", value)
		}
	}
}

func TestCapabilityEffectsDefaultAndStrictFields(t *testing.T) {
	var effects Effects
	if err := Decode([]byte(`{"kind":"read","approval":"none","retry":"read_only"}`), &effects); err != nil {
		t.Fatal(err)
	}
	if effects.Incidental == nil {
		t.Fatal("SDK default must serialize as an array")
	}
	if Decode([]byte(`{"kind":"read","approval":"none","retry":"read_only","hostCode":"x"}`), &effects) == nil {
		t.Fatal("unknown effects field accepted")
	}
}

func TestCapabilityRouteRejectsFieldsFromAnotherRoute(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"backend","connectionId":"10000000-0000-4000-8000-000000000001","origins":[]}`,
		`{"kind":"browser","origins":["https://example.com"],"adapter":""}`,
		`{"kind":"browser","origins":["https://example.com"],"hints":null}`,
	} {
		var route Route
		if Decode([]byte(raw), &route) == nil {
			t.Fatalf("accepted conflicting route: %s", raw)
		}
	}
	var effects Effects
	if Decode([]byte(`{"kind":"read","approval":"none","retry":"read_only","incidental":null}`), &effects) == nil {
		t.Fatal("accepted null incidental effects")
	}
}

func TestBuiltinSemanticContractsCannotBeWeakened(t *testing.T) {
	for name, versions := range builtinContracts {
		for _, definition := range versions {
			if err := definition.Validate(); err != nil {
				t.Fatalf("generated %s: %v", name, err)
			}
			weak := definition
			weak.Effects.Kind = "read"
			weak.Effects.Approval = "none"
			weak.Effects.Retry = "read_only"
			if weak.Validate() == nil {
				t.Fatalf("provider weakened %s", name)
			}
			definition.Version++
			if definition.Validate() == nil {
				t.Fatalf("provider introduced unreviewed built-in version for %s", name)
			}
		}
	}
}
