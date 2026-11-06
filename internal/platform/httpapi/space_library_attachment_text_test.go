package api

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/misty-org/misty-server/internal/workflow"
)

// testPDF builds a small real PDF: a content stream per page, with a cross-reference table.
func testPDF(pages ...string) []byte {
	var out bytes.Buffer
	var offsets []int
	object := func(body string) {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	out.WriteString("%PDF-1.4\n")
	object("<< /Type /Catalog /Pages 2 0 R >>")
	kids := make([]string, len(pages))
	for index := range pages {
		kids[index] = fmt.Sprintf("%d 0 R", 4+index*2)
	}
	object(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)))
	object("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for index, text := range pages {
		object(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+index*2))
		stream := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET"
		object(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	start := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, start)
	return out.Bytes()
}

func TestPDFTextKeepsItsPagesInOrder(t *testing.T) {
	text, err := extractPDFText(testPDF("First page", "", "Third page"))
	if err != nil {
		t.Fatal(err)
	}
	first, third := strings.Index(text, "[Page 1]"), strings.Index(text, "[Page 3]")
	if first < 0 || third < first || !strings.Contains(text, "First page") || !strings.Contains(text, "Third page") {
		t.Fatalf("text = %q", text)
	}
	if strings.Contains(text, "[Page 2]") {
		t.Fatalf("an empty page got a heading: %q", text)
	}
}

func TestPDFWithNoTextIsUnsupported(t *testing.T) {
	if _, err := extractPDFText(testPDF("")); !errors.Is(err, workflow.ErrUnsupportedContent) {
		t.Fatalf("err = %v", err)
	}
}

// The library panics on some malformed files. An attachment must only ever fail as an error.
func TestBrokenPDFsFailWithoutPanicking(t *testing.T) {
	whole := testPDF("Some text")
	stream := bytes.Index(whole, []byte("BT /F1"))
	broken := map[string][]byte{
		"empty":       {},
		"not a pdf":   []byte("hello"),
		"header only": []byte("%PDF-1.4\n"),
		"truncated":   whole[:len(whole)/2],
		"bad xref":    bytes.Replace(whole, []byte("xref"), []byte("nope"), 1),
		"bad stream":  append(append(append([]byte{}, whole[:stream]...), "( [ << garbage"...), whole[stream+20:]...),
		"self parent": bytes.Replace(whole, []byte("/Kids ["), []byte("/Parent 2 0 R /Kids [2 0 R "), 1),
	}
	for name, data := range broken {
		t.Run(name, func(t *testing.T) {
			if text, err := extractPDFText(data); err == nil && strings.TrimSpace(text) == "" {
				t.Fatal("no error and no text")
			}
		})
	}
}

func TestPDFTextStopsAtItsLimit(t *testing.T) {
	text, err := extractPDFTextLimited(testPDF(strings.Repeat("a", 500), strings.Repeat("b", 500)), 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(text) > 300 {
		t.Fatalf("len = %d", len(text))
	}
}

func TestPDFTextHasNoNULs(t *testing.T) {
	if got := cleanPDFText("Two\x00words"); got != "Two words" {
		t.Fatalf("text = %q", got)
	}
}
