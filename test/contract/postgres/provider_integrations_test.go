package db

import (
	"context"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestProviderCredentialOAuthSaveAndReconnect(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Provider Owner", "provider-owner@example.com", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Provider connections")
	credential := ProviderCredential{
		SpaceID:        space.ID,
		UserID:         owner.ID,
		Provider:       "notion",
		Ciphertext:     []byte("encrypted-token"),
		Nonce:          []byte("nonce"),
		KeyVersion:     1,
		AccountID:      "workspace-1",
		AccountDisplay: "Misty Test Workspace",
	}

	first, err := database.SaveProviderCredential(ctx, credential, credential.AccountDisplay, []string{"read_content"})
	if err != nil {
		t.Fatalf("first SaveProviderCredential() error = %v", err)
	}
	if first.Status != "active" || first.Provider != "notion" {
		t.Fatalf("first integration = %#v", first)
	}
	loaded, err := database.ProviderCredential(ctx, owner.ID, space.ID, first.ID)
	if err != nil || loaded.AccountID != credential.AccountID {
		t.Fatalf("ProviderCredential() = %#v, %v", loaded, err)
	}

	credential.Ciphertext = []byte("refreshed-encrypted-token")
	reconnected, err := database.SaveProviderCredential(ctx, credential, credential.AccountDisplay, []string{"read_content"})
	if err != nil {
		t.Fatalf("reconnect SaveProviderCredential() error = %v", err)
	}
	if reconnected.ID != first.ID {
		t.Fatalf("reconnect integration ID = %q, want %q", reconnected.ID, first.ID)
	}
}
