package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestUnifiedCloudBindingReusesAccountAndPropagatesHealth(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Cloud Owner", "cloud-owner@example.com", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	account, err := database.SaveConnectedAccount(ctx, ConnectedAccount{
		UserID: owner.ID, Provider: "google", AccountID: "google-cloud-user",
		AccountDisplay: "cloud-owner@example.com", CredentialCiphertext: []byte("sealed-token"),
		CredentialNonce: []byte("nonce"), KeyVersion: 1, Capabilities: []string{"files"},
		GrantedScopes: []string{"https://www.googleapis.com/auth/drive"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := database.BindConnectedAccountCloudConnection(ctx, owner.ID, *account, "drive", "Work Drive", 1)
	if err != nil {
		t.Fatalf("BindConnectedAccountCloudConnection() error = %v", err)
	}
	if bound.ConnectedAccountID != account.ID || bound.AccountID != account.AccountID || bound.Provider != "drive" {
		t.Fatalf("bound cloud connection = %#v", bound)
	}

	// Rebinding the same name is non-destructive and keeps one reusable remote.
	rebound, err := database.BindConnectedAccountCloudConnection(ctx, owner.ID, *account, "drive", "Work Drive", 1)
	if err != nil || rebound.ID != bound.ID {
		t.Fatalf("rebound cloud connection = %#v, %v", rebound, err)
	}
	items, err := database.CloudConnections(ctx, owner.ID)
	if err != nil || len(items) != 1 || items[0].ConnectedAccountID != account.ID {
		t.Fatalf("CloudConnections() = %#v, %v", items, err)
	}

	if err := database.SetConnectedAccountHealth(ctx, owner.ID, account.ID, "needs_attention", "refresh_failed"); err != nil {
		t.Fatal(err)
	}
	unhealthy, err := database.CloudConnection(ctx, owner.ID, bound.ID)
	if err != nil || unhealthy.Status != "needs_attention" || unhealthy.LastErrorCode != "refresh_failed" {
		t.Fatalf("unhealthy cloud connection = %#v, %v", unhealthy, err)
	}
}

func TestUnifiedCloudBindingEnforcesAccountOwnershipAndCapability(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Binding Owner", "binding-owner@example.com", "correct horse battery staple")
	other, _ := database.CreateUser("Other User", "other-cloud@example.com", "correct horse battery staple")
	account, err := database.SaveConnectedAccount(ctx, ConnectedAccount{
		UserID: owner.ID, Provider: "google", AccountID: "owned-google-account",
		AccountDisplay: "binding-owner@example.com", CredentialCiphertext: []byte("sealed-token"),
		CredentialNonce: []byte("nonce"), KeyVersion: 1, Capabilities: []string{"mail"},
		GrantedScopes: []string{"gmail.modify"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.BindConnectedAccountCloudConnection(ctx, owner.ID, *account, "drive", "No Files", 0); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("mail-only binding error = %v, want ErrSpaceForbidden", err)
	}
	account.Capabilities = []string{"files"}
	if _, err := database.BindConnectedAccountCloudConnection(ctx, other.ID, *account, "drive", "Stolen", 0); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("cross-user binding error = %v, want ErrSpaceForbidden", err)
	}
}

func TestCloudCredentialHandoffIsPrivateAtomicAndSingleUse(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Handoff Owner", "handoff-owner@example.com", "correct horse battery staple")
	account, _ := database.SaveConnectedAccount(ctx, ConnectedAccount{
		UserID: owner.ID, Provider: "dropbox", AccountID: "dropbox-account",
		AccountDisplay: "Dropbox", CredentialCiphertext: []byte("sealed-token"),
		CredentialNonce: []byte("nonce"), KeyVersion: 1, Capabilities: []string{"files"},
	})
	cloud, err := database.BindConnectedAccountCloudConnection(ctx, owner.ID, *account, "dropbox", "Dropbox", 0)
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	expiresAt := time.Now().UTC().Add(time.Minute)
	if err := database.CreateCloudCredentialHandoff(ctx, hash, CloudCredentialHandoff{
		UserID: owner.ID, CloudConnectionID: cloud.ID, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := database.ConsumeCloudCredentialHandoff(ctx, hash)
	if err != nil || claim.UserID != owner.ID || claim.CloudConnectionID != cloud.ID {
		t.Fatalf("ConsumeCloudCredentialHandoff() = %#v, %v", claim, err)
	}
	if _, err := database.ConsumeCloudCredentialHandoff(ctx, hash); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("replayed handoff error = %v, want ErrSpaceNotFound", err)
	}

	expiredHash := strings.Repeat("b", 64)
	if err := database.CreateCloudCredentialHandoff(ctx, expiredHash, CloudCredentialHandoff{
		UserID: owner.ID, CloudConnectionID: cloud.ID, ExpiresAt: time.Now().UTC().Add(-time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ConsumeCloudCredentialHandoff(ctx, expiredHash); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("expired handoff error = %v, want ErrSpaceNotFound", err)
	}
}
