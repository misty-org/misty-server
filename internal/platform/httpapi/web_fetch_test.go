package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWebFetchResultSendsRefusedPagesToATab(t *testing.T) {
	for _, tc := range []struct {
		page    string
		refused bool
	}{
		{`{"url":"https://www.moneysavingexpert.com/x","title":"Just a moment...","status":403,"text":""}`, true},
		{`{"url":"https://a.test","title":"Attention Required! | Cloudflare","status":200,"text":""}`, true},
		{`{"url":"https://a.test","title":"Opening hours","status":200,"text":"Open 9 to 5"}`, false},
	} {
		raw, err := webFetchResult(json.RawMessage(tc.page))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(raw), "own browser tab"); got != tc.refused {
			t.Fatalf("%s: note shown = %v, want %v", tc.page, got, tc.refused)
		}
	}
}
