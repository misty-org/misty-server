package api

import (
	"github.com/kannachi323/misty/server/internal/apprpc"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"net/http"
)

// OfficialAppRPC accepts only a live installed-App session, never a full
// account credential supplied by a downloaded component.
func OfficialAppRPC(database *db.Database, router http.Handler, prefix string) http.HandlerFunc {
	handler := apprpc.Handler{
		Prefix: prefix, Dispatch: router,
		Authenticate: func(w http.ResponseWriter, r *http.Request) (apprpc.Identity, bool) {
			session, ok := appRuntimeSession(w, r, database)
			if !ok {
				return apprpc.Identity{}, false
			}
			if session.SpaceID != "" {
				member, err := database.IsSpaceMember(r.Context(), session.UserID, session.SpaceID)
				if err != nil {
					writeOfficialAppError(w, err)
					return apprpc.Identity{}, false
				}
				if !member {
					writeJSON(w, http.StatusForbidden, map[string]string{"code": "app_runtime_forbidden"})
					return apprpc.Identity{}, false
				}
			}
			return apprpc.Identity{AppID: session.AppID, AccountID: session.UserID, SpaceID: session.SpaceID, Scopes: session.Scopes}, true
		},
		Authorize: func(identity apprpc.Identity, method, path string) bool {
			return TestingAuthorizeAppRuntimeRequest(db.AppRuntimeSession{AppID: identity.AppID, UserID: identity.AccountID, SpaceID: identity.SpaceID, Scopes: identity.Scopes}, method, path)
		},
	}
	return handler.ServeHTTP
}
