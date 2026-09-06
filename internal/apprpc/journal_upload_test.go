package apprpc

import (
	"net/http"
	"strings"
	"testing"
)

func TestJournalUploadCredentialHasMethodBoundary(t *testing.T) {
	const header = "X-Misty-Library-Upload-Token"
	source := http.Header{header: []string{"upload-credential"}}
	for _, method := range []string{"notes.assets.finalize", "drawings.assets.finalize", "notes.create", "notes.assets.reserve", "drawings.assets.download"} {
		destination := make(http.Header)
		if err := forwardJournalUploadCredential(destination, source, method); err != nil {
			t.Fatal(err)
		}
		want := ""
		if strings.HasSuffix(method, ".finalize") {
			want = "upload-credential"
		}
		if destination.Get(header) != want {
			t.Fatalf("method %s forwarded incorrect credential", method)
		}
	}
	for _, values := range [][]string{{""}, {" "}, {"duplicate", "second"}, {"duplicate, second"}, {"two words"}, {"injected\r\nheader"}, {strings.Repeat("x", 1025)}} {
		if err := forwardJournalUploadCredential(make(http.Header), http.Header{header: values}, "notes.assets.finalize"); err == nil {
			t.Fatal("accepted invalid credential")
		}
	}
}
