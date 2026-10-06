package db

import (
	"testing"

	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// invocationOwnerFixture opens the test database with one account that owns
// the runs a test creates.
func invocationOwnerFixture(t *testing.T) (*Database, string) {
	t.Helper()
	database := openTestDatabase(t)
	user, err := database.CreateUser("Invocation owner", "invocation-owner-"+uuid.NewString()+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	return database, user.ID
}
