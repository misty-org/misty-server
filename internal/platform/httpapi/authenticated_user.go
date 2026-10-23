package api

import (
	"github.com/kannachi323/misty/server/internal/accounts"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"net/http"
)

func authenticatedUser(w http.ResponseWriter, r *http.Request, _ *db.Database) (string, bool) {
	return accounts.AuthenticatedUser(w, r)
}
