package agent

import "testing"

func TestSensesChooseGatewayModelsByTypeAndTags(t *testing.T) {
	cases := []struct {
		kind string
		item gatewayCatalogItem
		want bool
	}{
		{"thinking", gatewayCatalogItem{Type: "language", Tags: []string{"tool-use"}}, true},
		{"thinking", gatewayCatalogItem{Type: "language", Tags: []string{"vision"}}, false},
		{"seeing", gatewayCatalogItem{Type: "language", Tags: []string{"vision", "structured-output"}}, true},
		{"seeing", gatewayCatalogItem{Type: "language", Tags: []string{"vision"}}, false},
		{"listening", gatewayCatalogItem{Type: "transcription"}, true},
		{"listening", gatewayCatalogItem{Type: "transcription", Tags: []string{"websocket-transcription"}}, false},
		{"speaking", gatewayCatalogItem{Type: "realtime"}, true},
		{"speaking", gatewayCatalogItem{Type: "speech"}, false},
		{"thinking", gatewayCatalogItem{Type: "image", Tags: []string{"tool-use"}}, false},
	}
	for _, c := range cases {
		if got := senseFits(c.kind, c.item); got != c.want {
			t.Errorf("senseFits(%s, %+v) = %v, want %v", c.kind, c.item, got, c.want)
		}
	}
}
