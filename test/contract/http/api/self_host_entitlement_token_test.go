package api_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/entitlement"
)

func TestSignedEntitlementVerificationAndExpiry(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2_000_000_000, 0).UTC()
	claims := entitlement.Claims{Subject: "license_subject", Status: "eligible", IssuedAt: now.Unix(),
		ExpiresAt: now.Add(entitlement.MaxLifetime).Unix(), TokenID: "proof_1", SchemaVersion: entitlement.SchemaVersion,
		Issuer: entitlement.Issuer, Audience: entitlement.Audience}
	token, err := (entitlement.Signer{PrivateKey: privateKey, KeyID: "test"}).Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := entitlement.Verify(token, map[string]ed25519.PublicKey{"test": publicKey}, now)
	if err != nil || verified.Subject != claims.Subject {
		t.Fatalf("Verify() = %#v, %v", verified, err)
	}
	if _, err := entitlement.Verify(token, map[string]ed25519.PublicKey{"test": publicKey}, now.Add(entitlement.MaxLifetime)); !errors.Is(err, entitlement.ErrExpired) {
		t.Fatalf("expired Verify() error = %v", err)
	}
	if _, err := entitlement.Verify(token+"tampered", map[string]ed25519.PublicKey{"test": publicKey}, now); !errors.Is(err, entitlement.ErrInvalid) {
		t.Fatalf("tampered Verify() error = %v", err)
	}
}

func TestEntitlementCannotExceedSevenDays(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Unix(2_000_000_000, 0).UTC()
	claims := entitlement.Claims{Subject: "license_subject", Status: "eligible", IssuedAt: now.Unix(),
		ExpiresAt: now.Add(entitlement.MaxLifetime + time.Second).Unix(), TokenID: "proof_1",
		SchemaVersion: entitlement.SchemaVersion, Issuer: entitlement.Issuer, Audience: entitlement.Audience}
	token, err := (entitlement.Signer{PrivateKey: privateKey, KeyID: "test"}).Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entitlement.Verify(token, map[string]ed25519.PublicKey{"test": publicKey}, now); !errors.Is(err, entitlement.ErrInvalid) {
		t.Fatalf("Verify() error = %v", err)
	}
}
