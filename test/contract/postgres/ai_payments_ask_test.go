package db

import (
	"context"
	"testing"
)

func TestAskBeforePayingStartsOnAndChangesAlone(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("payments-ask@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	settings, _, err := database.AISettings(ctx, user.ID)
	if err != nil || !settings.PaymentsAsk || settings.AppActionsAsk {
		t.Fatalf("defaults = %#v, %v", settings, err)
	}

	off := false
	if err := database.SetAIAppActionsAsk(ctx, user.ID, nil, &off); err != nil {
		t.Fatal(err)
	}
	settings, _, err = database.AISettings(ctx, user.ID)
	if err != nil || settings.PaymentsAsk || settings.AppActionsAsk {
		t.Fatalf("after turning payments off = %#v, %v", settings, err)
	}

	on := true
	if err := database.SetAIAppActionsAsk(ctx, user.ID, nil, &on); err != nil {
		t.Fatal(err)
	}
	settings, _, err = database.AISettings(ctx, user.ID)
	if err != nil || !settings.PaymentsAsk || settings.AppActionsAsk {
		t.Fatalf("after turning payments back on = %#v, %v", settings, err)
	}
}
