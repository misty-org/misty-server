package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/misty-org/misty-server/internal/platform/db"
)

func TestSiteGrantOriginsAreExact(t *testing.T) {
	service := &SpacesService{}
	for origin, valid := range map[string]bool{
		"https://mail.example.com":      true,
		"https://mail.example.com:8443": true,
		"http://mail.example.com":       false,
		"https://mail.example.com/x":    false,
		"https://*.example.com":         false,
		"*":                             false,
		"":                              false,
	} {
		request := &db.AgentAppRequest{GrantAction: "browser.send", GrantScopeKind: "origin", GrantScopeID: origin, GrantConstraints: json.RawMessage(`{}`)}
		if err := service.validateGrantScope(context.Background(), "user", request); (err == nil) != valid {
			t.Errorf("origin %q valid=%v, got %v", origin, valid, err)
		}
	}
	payment := &db.AgentAppRequest{GrantAction: "payment", GrantScopeKind: "merchant", GrantScopeID: "stripe", GrantConstraints: json.RawMessage(`{}`)}
	if service.validateGrantScope(context.Background(), "user", payment) == nil {
		t.Fatal("a payment grant without a cap must be refused")
	}
}
