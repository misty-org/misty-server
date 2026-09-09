package api

import (
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/appcatalog"
	"github.com/kannachi323/misty/server/internal/apprpc"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSDKServerMethodsHaveAnAuthorizedAppAndStaySpaceBound(t *testing.T) {
	for name, method := range apprpc.Methods() {
		t.Run(name, func(t *testing.T) {
			path := map[string]string{}
			for _, part := range strings.Split(method.Path, "/") {
				if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
					key := strings.Trim(part, "{}")
					if key != "spaceID" {
						path[key] = "resource_1"
						if key == "requestID" {
							path[key] = "10000000-0000-4000-8000-000000000001"
						}
						if key == "providerID" {
							path[key] = "example.habits/backend"
						}
					}
				}
			}
			target, err := apprpc.Resolve(apprpc.Request{Protocol: 2, Method: name, Params: apprpc.Params{Path: path}}, "space_1")
			if err != nil {
				t.Fatal(err)
			}
			authorized := false
			for _, app := range appcatalog.All() {
				session := db.AppRuntimeSession{AppID: app.ID, SpaceID: "space_1", Scopes: app.Scopes}
				if TestingAuthorizeAppRuntimeRequest(session, target.Verb, target.Path) {
					authorized = true
				}
				session.SpaceID = "space_2"
				if strings.Contains(method.Path, "{spaceID}") && TestingAuthorizeAppRuntimeRequest(session, target.Verb, target.Path) {
					t.Errorf("%s escaped the bound Space", app.ID)
				}
				session.SpaceID = "space_1"
				session.Scopes = nil
				if TestingAuthorizeAppRuntimeRequest(session, target.Verb, target.Path) {
					t.Errorf("%s ran without a permission grant", app.ID)
				}
			}
			if strings.HasPrefix(name, "capabilities.") {
				// SDK lifecycle permissions belong to a reviewed independent
				// installation; they are not implied by a catalog app's grants.
				independent := sdkContractCapabilitySession()
				if TestingAuthorizeAppRuntimeRequest(independent, target.Verb, target.Path) {
					authorized = true
				}
				independent.Scopes = nil
				if TestingAuthorizeAppRuntimeRequest(independent, target.Verb, target.Path) {
					t.Fatal("SDK method escaped its installed scope ceiling")
				}
			}
			if !authorized {
				t.Fatal("No appropriately scoped App can use this SDK method")
			}
		})
	}
}

func sdkContractCapabilitySession() db.AppRuntimeSession {
	return db.AppRuntimeSession{AppID: "example.habits", Scopes: []string{"capabilities.providers.write", "capabilities.read", "capabilities.invoke"}}
}
