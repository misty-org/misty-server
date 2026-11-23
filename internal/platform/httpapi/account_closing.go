package api

import (
	"context"

	"github.com/google/uuid"

	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
)

type AccountClosings struct {
	database *db.Database
}

func NewAccountClosings(database *db.Database) *AccountClosings {
	return &AccountClosings{database: database}
}

func (c *AccountClosings) Process(ctx context.Context, limit int) (int, error) {
	due, err := c.database.DueClosedAccounts(ctx, limit)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, userID := range due {
		token, err := security.GenerateSecureToken()
		if err != nil {
			return done, err
		}
		started, err := c.database.BeginClosedAccountDeletion(ctx, userID, "deletion_"+uuid.NewString(), security.HashToken(token))
		if err != nil {
			return done, err
		}
		if started {
			done++
		}
	}
	return done, nil
}
