package unit

import (
	"archive/zip"
	"bytes"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	"testing"
)

func TestExtractDOCXTextIsDeterministic(t *testing.T) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	part, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(`<w:document xmlns:w="urn:test"><w:body><w:p><w:r><w:t>Hello</w:t></w:r><w:r><w:tab/><w:t>Agent</w:t></w:r></w:p><w:p><w:r><w:t>Context</w:t></w:r></w:p></w:body></w:document>`)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	text, err := api.TestingExtractDOCXText(buffer.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if text != "HelloAgent\nContext" {
		t.Fatalf("unexpected extraction: %q", text)
	}
}
