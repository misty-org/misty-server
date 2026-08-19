package db

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestConnectedAccountOAuthStateIsPrivateAndSingleUse(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Connection Owner", "connection-owner@example.com", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	stateHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	want := ConnectedAccountOAuthState{
		UserID: owner.ID, Provider: "google", Capabilities: []string{"mail"},
		RequestedScopes:    []string{"openid", "gmail.modify"},
		VerifierCiphertext: []byte("sealed-verifier"), VerifierNonce: []byte("nonce"),
		ReturnTo: "/inbox", ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	}
	if err := database.CreateConnectedAccountOAuthState(ctx, stateHash, want); err != nil {
		t.Fatalf("CreateConnectedAccountOAuthState() error = %v", err)
	}
	got, err := database.ConsumeConnectedAccountOAuthState(ctx, stateHash)
	if err != nil || got.UserID != owner.ID || got.Provider != "google" || len(got.Capabilities) != 1 {
		t.Fatalf("ConsumeConnectedAccountOAuthState() = %#v, %v", got, err)
	}
	if _, err := database.ConsumeConnectedAccountOAuthState(ctx, stateHash); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("replayed OAuth state error = %v, want ErrSpaceNotFound", err)
	}
}

func TestConnectedAccountReconnectAndRevoke(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Mail Owner", "mail-owner@example.com", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	first, err := database.SaveConnectedAccount(ctx, ConnectedAccount{
		UserID: owner.ID, Provider: "google", AccountID: "google-user-1",
		AccountDisplay: "owner@example.com", CredentialCiphertext: []byte("sealed-token-1"),
		CredentialNonce: []byte("nonce-1"), KeyVersion: 1,
		Capabilities: []string{"mail"}, GrantedScopes: []string{"gmail.modify"},
	})
	if err != nil {
		t.Fatalf("SaveConnectedAccount() error = %v", err)
	}
	reconnected, err := database.SaveConnectedAccount(ctx, ConnectedAccount{
		UserID: owner.ID, Provider: "google", AccountID: "google-user-1",
		AccountDisplay: "owner@example.com", CredentialCiphertext: []byte("sealed-token-2"),
		CredentialNonce: []byte("nonce-2"), KeyVersion: 1,
		Capabilities: []string{"mail"}, GrantedScopes: []string{"gmail.modify", "gmail.send"},
	})
	if err != nil || reconnected.ID != first.ID || len(reconnected.GrantedScopes) != 2 {
		t.Fatalf("reconnected account = %#v, %v", reconnected, err)
	}
	items, err := database.ConnectedAccounts(ctx, owner.ID)
	if err != nil || len(items) != 1 || items[0].Status != "active" {
		t.Fatalf("ConnectedAccounts() = %#v, %v", items, err)
	}
	if err := database.SetConnectedAccountHealth(ctx, owner.ID, first.ID, "needs_attention", "refresh_failed"); err != nil {
		t.Fatalf("SetConnectedAccountHealth() error = %v", err)
	}
	if err := database.RevokeConnectedAccount(ctx, owner.ID, first.ID); err != nil {
		t.Fatalf("RevokeConnectedAccount() error = %v", err)
	}
	items, err = database.ConnectedAccounts(ctx, owner.ID)
	if err != nil || len(items) != 0 {
		t.Fatalf("ConnectedAccounts() after revoke = %#v, %v", items, err)
	}
}
