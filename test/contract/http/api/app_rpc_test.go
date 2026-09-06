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
			if !authorized {
				t.Fatal("No catalog App can use this SDK method")
			}
		})
	}
}
