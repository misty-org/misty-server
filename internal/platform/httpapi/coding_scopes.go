package api

import (
	"context"
	"regexp"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

var repoScope = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)

// validateCodingScope checks a project or repository scope before it is
// remembered: a project must be one the person attached from one of their own
// trusted devices; a repository is the project's remote as owner/name.
func (s *SpacesService) validateCodingScope(ctx context.Context, user string, kind agent.ScopeKind, id string) error {
	switch kind {
	case agent.ScopeProject:
		owned, err := s.database.ProjectContextOwned(ctx, user, id)
		if err != nil {
			return err
		}
		if !owned {
			return db.ErrSpaceForbidden
		}
		return nil
	case agent.ScopeRepo:
		if !repoScope.MatchString(id) {
			return db.ErrSpaceInvalid
		}
		return nil
	}
	return db.ErrSpaceForbidden
}
