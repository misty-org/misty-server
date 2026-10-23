package browsersync

import (
	"encoding/base64"
	"testing"
)

// A transition signed by the native sync crate (`VaultRoot::sign_key_transition`)
// for a wrapper made by `VaultRoot::wrap_account`. If either side changes how it
// hashes or signs, this vector stops verifying.
const (
	vectorVault      = "6f1c2a2e-5b1e-4c55-9c3d-0b6f7b1a8e21"
	vectorRoot       = "YGA5WsPUgs5kRWX0tUeiDwp8Vs0fhRJkumC8ENpBJkU="
	vectorSalt       = "0vsfy2TnSFhe2W71r0awHQ=="
	vectorNonce      = "6dGK5Z+ysnGjNCKt"
	vectorCiphertext = "Tn5LmOYB/9cyuAj4QPpC2nMTu0Crz8Y8ie4fhpwbGDJCxfHElG8qHVqJp2ObqEr1"
	vectorSignature  = "M/06g4kgGHl4VbGI9d8PdYtpS5zlK09RNDjqa+GBs/Mj3V7o6CtLBatrRpXxhZg/yOTlk3+snPD0A9OMZVAUCw=="
)

func vectorParts(t *testing.T) ([]byte, SyncKeyEnvelope, SyncKeyTransition) {
	t.Helper()
	root, err := base64.StdEncoding.DecodeString(vectorRoot)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.StdEncoding.DecodeString(vectorSignature)
	if err != nil {
		t.Fatal(err)
	}
	envelope := SyncKeyEnvelope{Version: 1, KDF: SyncKDFAccount, Salt: vectorSalt, Nonce: vectorNonce, Ciphertext: vectorCiphertext}
	return root, envelope, SyncKeyTransition{Generation: 1, Signature: signature}
}

func TestKeyTransitionVerifiesWhatTheNativeCrateSigned(t *testing.T) {
	root, envelope, transition := vectorParts(t)
	if !envelope.Valid() {
		t.Fatal("the account wrapper should be a valid envelope")
	}
	if !transition.valid(root, vectorVault, envelope) {
		t.Fatal("the native signature should verify")
	}
}

func TestKeyTransitionCoversOneVaultWrapperAndGeneration(t *testing.T) {
	root, envelope, transition := vectorParts(t)
	other := envelope
	other.Ciphertext = vectorNonce + vectorNonce + vectorNonce + vectorNonce + vectorNonce + vectorNonce + "AAAA"
	if transition.valid(root, vectorVault, other) {
		t.Fatal("a different wrapper must not verify")
	}
	if transition.valid(root, "11111111-2222-4333-8444-555555555555", envelope) {
		t.Fatal("a different vault must not verify")
	}
	raised := transition
	raised.Generation = 2
	if raised.valid(root, vectorVault, envelope) {
		t.Fatal("a different generation must not verify")
	}
	stranger := append([]byte(nil), root...)
	stranger[0] ^= 1
	if transition.valid(stranger, vectorVault, envelope) {
		t.Fatal("another root must not verify")
	}
	zero := transition
	zero.Generation = 0
	if zero.valid(root, vectorVault, envelope) {
		t.Fatal("generation zero is the original wrapper and cannot be signed")
	}
}

func TestAccountWrapperIsAcceptedAtTheStorageBoundary(t *testing.T) {
	_, envelope, _ := vectorParts(t)
	legacy := envelope
	legacy.KDF = SyncKDFLegacy
	if !legacy.Valid() || !envelope.Valid() {
		t.Fatal("both wrapper kinds are valid envelopes")
	}
	unknown := envelope
	unknown.KDF = "argon2id-m1-t1-p1"
	if unknown.Valid() {
		t.Fatal("an unknown KDF must be refused")
	}
}

func TestNewVaultsMustUseTheAccountWrapper(t *testing.T) {
	_, envelope, _ := vectorParts(t)
	legacy := envelope
	legacy.KDF = SyncKDFLegacy
	if PasswordVaultKeys(legacy).valid() {
		t.Fatal("a new vault was allowed the sync-secret wrapper")
	}
	if !PasswordVaultKeys(envelope).valid() {
		t.Fatal("a new vault was refused the account wrapper")
	}
}
