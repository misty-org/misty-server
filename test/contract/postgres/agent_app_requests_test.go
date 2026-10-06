package db

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAgentAppRequestsAreOwnedSingleUseAndRemembered(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Apps owner", "apps-"+uuid.NewString()+"@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other", "apps-"+uuid.NewString()+"@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	approval := AgentAppRequest{OwnerUserID: owner.ID, RunID: "invocation_" + uuid.NewString(), Kind: "approve", Subject: "GMAIL_SEND_EMAIL", ArgumentsHash: "hash-a", Title: "Gmail · Send Email", Summary: "to: a@example.com"}
	first, err := database.OpenAgentAppRequest(ctx, approval, 15*time.Minute)
	if err != nil || first.State != "pending" {
		t.Fatalf("open = %+v, %v", first, err)
	}
	again, err := database.OpenAgentAppRequest(ctx, approval, 15*time.Minute)
	if err != nil || again.ID != first.ID {
		t.Fatalf("a waiting call opened a second card: %+v, %v", again, err)
	}
	if _, err := database.AgentAppRequest(ctx, other.ID, first.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("another account read the request: %v", err)
	}
	if _, err := database.DecideAgentAppRequest(ctx, other.ID, first.ID, "approved"); err == nil {
		t.Fatal("another account approved the request")
	}
	if decided, err := database.DecideAgentAppRequest(ctx, owner.ID, first.ID, "approved"); err != nil || decided.State != "approved" {
		t.Fatalf("approve = %+v, %v", decided, err)
	}
	if used, err := database.ResolveAgentAppRequest(ctx, owner.ID, first.ID, "approved", "used"); err != nil || !used {
		t.Fatalf("consume = %v, %v", used, err)
	}
	if used, _ := database.ResolveAgentAppRequest(ctx, owner.ID, first.ID, "approved", "used"); used {
		t.Fatal("an approval was used twice")
	}
	next, err := database.OpenAgentAppRequest(ctx, approval, 15*time.Minute)
	if err != nil || next.ID == first.ID || next.State != "pending" {
		t.Fatalf("a repeated action reused a spent approval: %+v, %v", next, err)
	}
	if _, err := database.DecideAgentAppRequest(ctx, owner.ID, next.ID, "declined"); err != nil {
		t.Fatal(err)
	}
	if remembered, err := database.OpenAgentAppRequest(ctx, approval, 15*time.Minute); err != nil || remembered.ID != next.ID || remembered.State != "declined" {
		t.Fatalf("a declined action asked again at once: %+v, %v", remembered, err)
	}

	connect := AgentAppRequest{OwnerUserID: owner.ID, RunID: approval.RunID, Kind: "connect", Subject: "googledrive", Title: "Connect Google Drive"}
	card, err := database.OpenAgentAppRequest(ctx, connect, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.DecideAgentAppRequest(ctx, owner.ID, card.ID, "approved"); err == nil {
		t.Fatal("a connect card was approved instead of connected")
	}
	if done, err := database.ResolveAgentAppRequest(ctx, owner.ID, card.ID, "pending", "connected"); err != nil || !done {
		t.Fatalf("connect = %v, %v", done, err)
	}
}
