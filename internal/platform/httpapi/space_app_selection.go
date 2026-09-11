package api

import (
	"encoding/json"
	"fmt"
	"github.com/kannachi323/misty/server/internal/appcatalog"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Reviewed app versions are resolved on the server, never accepted as grants.
func reviewedSpaceApps(ids []string, permissions map[string]int) ([]db.AppInstallSpec, error) {
	out := []db.AppInstallSpec{}
	selected := map[string]bool{}
	for _, id := range ids {
		if selected[id] {
			return nil, db.ErrSpaceInvalid
		}
		selected[id] = true
		app, ok := appcatalog.Find(id)
		if !ok {
			return nil, db.ErrAppNotFound
		}
		if permissions[id] != app.PermissionVersion {
			return nil, fmt.Errorf("%w: review current app permissions", db.ErrSpaceConflict)
		}
		metadata, _ := json.Marshal(app)
		out = append(out, db.AppInstallSpec{ID: id, Version: app.Version, PermissionVersion: app.PermissionVersion, Scopes: app.Scopes, Metadata: metadata})
	}
	for _, spec := range out {
		var metadata struct {
			RequiresApps []string `json:"requires_apps"`
		}
		_ = json.Unmarshal(spec.Metadata, &metadata)
		for _, required := range metadata.RequiresApps {
			if !selected[required] {
				return nil, db.ErrAppDependencies
			}
		}
	}
	return out, nil
}
