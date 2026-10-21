package api

import (
	"encoding/json"
	"testing"
)

func TestExtractedLinksArePageLinks(t *testing.T) {
	page := "# rainbow_bebe\n[Prom dress](/products/rainbow_bebe-prom-dress-goth-grunge-fairy-f40f/)\n" +
		"[Green corset](https://www.depop.com/products/rainbow_bebe-green-fairy-dress-prom-corset-f9ed/)\n"
	data := json.RawMessage(`{"items":[
		{"url":"https://www.depop.com/products/rainbzw_bebe-prom-dress-goth-grunge-fairy-f40f/"},
		{"url":"/products/rainbow_bebe-green-fairy-dress-prom-corset-f9ed/"},
		{"url":"https://www.depop.com/products/made-up-listing-1234/"},
		{"name":"Prom dress, $45"}
	]}`)
	out, corrected, removed := verifyExtractedLinks(data, page, "https://www.depop.com/rainbow_bebe/")
	var got struct {
		Items []map[string]string `json:"items"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Items[0]["url"] != "https://www.depop.com/products/rainbow_bebe-prom-dress-goth-grunge-fairy-f40f/" {
		t.Fatalf("a near copy should be put right, got %q", got.Items[0]["url"])
	}
	if got.Items[1]["url"] != "https://www.depop.com/products/rainbow_bebe-green-fairy-dress-prom-corset-f9ed/" {
		t.Fatalf("a page path should resolve to the page's link, got %q", got.Items[1]["url"])
	}
	if got.Items[2]["url"] != "" || len(removed) != 1 {
		t.Fatalf("a link the page doesn't hold should be removed, got %q and %v", got.Items[2]["url"], removed)
	}
	if got.Items[3]["name"] != "Prom dress, $45" || corrected != 1 {
		t.Fatalf("plain text stays and one link is corrected, got %v and %d", got.Items[3], corrected)
	}
}
