package api

import (
	"regexp"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/appcatalog"
	"github.com/kannachi323/misty/server/internal/apprpc"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSDKMethodsMatchOfficialAppCapabilities(t *testing.T) {
	placeholder := regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9]*)\}`)
	for name, method := range apprpc.Methods() {
		t.Run(name, func(t *testing.T) {
			appID := "planner"
			if strings.HasPrefix(name, "mail.") {
				appID = "inbox"
			}
			if strings.HasPrefix(name, "notes.") || strings.HasPrefix(name, "drawings.") {
				appID = "journal"
			}
			app, ok := appcatalog.Find(appID)
			if !ok {
				t.Fatalf("missing app %s", appID)
			}
			params := map[string]string{}
			for _, match := range placeholder.FindAllStringSubmatch(method.Path, -1) {
				params[match[1]] = "object_1"
			}
			params["spaceID"] = "space_1"
			target, err := apprpc.Resolve(apprpc.Request{Protocol: 2, Method: name, Params: apprpc.Params{Path: params}}, "space_1")
			if err != nil {
				t.Fatal(err)
			}
			for _, prefix := range []string{"", "/api", "/v1"} {
				session := db.AppRuntimeSession{AppID: appID, SpaceID: "space_1", Scopes: app.Scopes}
				if !TestingAuthorizeAppRuntimeRequest(session, target.Verb, prefix+target.Path) {
					t.Fatalf("catalog does not authorize %s %s", target.Verb, prefix+target.Path)
				}
				session.SpaceID = "another_space"
				if strings.Contains(method.Path, "{spaceID}") && TestingAuthorizeAppRuntimeRequest(session, target.Verb, prefix+target.Path) {
					t.Fatal("SDK method escaped its bound Space")
				}
				session.SpaceID, session.Scopes = "space_1", nil
				if TestingAuthorizeAppRuntimeRequest(session, target.Verb, prefix+target.Path) {
					t.Fatal("SDK method does not require a capability")
				}
			}
			params["spaceID"] = "another_space"
			if _, err := apprpc.Resolve(apprpc.Request{Protocol: 2, Method: name, Params: apprpc.Params{Path: params}}, "space_1"); err == nil {
				t.Fatal("RPC resolver accepted another Space, including an account-scoped method")
			}
		})
	}
}
