package api

import (
	"context"

	"github.com/google/uuid"

	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// AccountClosings is the account-deletion worker's part for closed accounts
// (POST /operator/v1/accounts/{id}/close): it starts the purge once the date
// comes. The server sends no email; the billing service, or the operator,
// tells the person.
type AccountClosings struct {
	database *db.Database
}

func NewAccountClosings(database *db.Database) *AccountClosings {
	return &AccountClosings{database: database}
}

// Process starts due deletions, returning how many it started.
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
