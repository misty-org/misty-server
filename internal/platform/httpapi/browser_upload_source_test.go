package api

import (
	"encoding/json"
	"testing"

	"github.com/kannachi323/misty/server/internal/capabilities"
)

func TestBrowserUploadSourceRequiresExactlyOneAuthorizedSource(t *testing.T) {
	for _, raw := range []string{
		`{"attachmentId":"attachment-one"}`,
		`{"downloadId":"download-one","sourceScopeId":"source-scope"}`,
	} {
		if _, err := parseBrowserUploadSource(json.RawMessage(raw)); err != nil {
			t.Fatalf("valid source %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{}`, `{"downloadId":"download-one"}`, `{"sourceScopeId":"source-scope"}`,
		`{"attachmentId":"one","downloadId":"two","sourceScopeId":"source-scope"}`,
		`{"attachmentId":"one","sourceScopeId":"source-scope"}`,
		`{"downloadId":"one","sourceScopeId":"short"}`,
		`{"attachmentId":17}`,
	} {
		if _, err := parseBrowserUploadSource(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid source %s", raw)
		}
	}
}

func TestBrowserUploadSchemaExcludesMixedSourcesBeforeDispatch(t *testing.T) {
	schema, err := capabilities.CompileSchema(browserAgentToolSchema("upload"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source string
		valid  bool
	}{
		{`"attachmentId":"attachment-one"`, true},
		{`"downloadId":"download-one","sourceScopeId":"source-scope"`, true},
		{`"attachmentId":"none","downloadId":"download-one","sourceScopeId":"source-scope"`, false},
		{`"attachmentId":"attachment-one","sourceScopeId":"source-scope"`, false},
		{`"downloadId":"download-one"`, false},
		{`"sourceScopeId":"source-scope"`, false},
	} {
		var value any
		raw := `{"scopeId":"destination-scope","documentId":"6955167e-0639-410e-b818-7f8d1dfd095b","elementRef":"file-input",` + tc.source + `}`
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(value); (err == nil) != tc.valid {
			t.Fatalf("source %s valid=%v: %v", tc.source, tc.valid, err)
		}
	}
}
