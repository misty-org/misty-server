package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type googleKeyServer struct {
	*httptest.Server
	keys     map[string]*rsa.PublicKey
	requests atomic.Int32
	status   atomic.Int32
}

func newGoogleKeyServer(t *testing.T, keys map[string]*rsa.PublicKey) *googleKeyServer {
	t.Helper()
	server := &googleKeyServer{keys: keys}
	server.status.Store(http.StatusOK)
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		server.requests.Add(1)
		if status := int(server.status.Load()); status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		entries := []map[string]string{}
		for id, key := range server.keys {
			entries = append(entries, map[string]string{
				"kid": id, "kty": "RSA", "alg": "RS256", "use": "sig",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			})
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": entries})
	}))
	t.Cleanup(server.Close)
	return server
}

func (s *googleKeyServer) keySet(now func() time.Time) *googleKeySet {
	return &googleKeySet{url: s.URL, client: s.Client(), now: now}
}

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func googleClaims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": "https://accounts.google.com", "aud": "client-id", "azp": "client-id", "sub": "subject",
		"email": "person@example.com", "email_verified": true, "nonce": "nonce", "exp": now.Add(time.Hour).Unix(),
	}
}

func signGoogle(t *testing.T, key *rsa.PrivateKey, id string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = id
	raw, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGoogleTokenThatsRealIsAccepted(t *testing.T) {
	now := time.Now()
	key := testRSAKey(t)
	server := newGoogleKeyServer(t, map[string]*rsa.PublicKey{"one": &key.PublicKey})
	identity, err := server.keySet(time.Now).validate(context.Background(), signGoogle(t, key, "one", googleClaims(now)), "client-id")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Subject != "subject" || identity.Audience != "client-id" || !validGoogleIdentity(identity, "nonce", "client-id") {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestGoogleTokenForgeriesAreRefused(t *testing.T) {
	now := time.Now()
	key, attacker := testRSAKey(t), testRSAKey(t)
	server := newGoogleKeyServer(t, map[string]*rsa.PublicKey{"one": &key.PublicKey})
	changed := func(change func(jwt.MapClaims)) jwt.MapClaims {
		claims := googleClaims(now)
		change(claims)
		return claims
	}
	noAlg, err := func() (string, error) {
		token := jwt.NewWithClaims(jwt.SigningMethodNone, googleClaims(now))
		token.Header["kid"] = "one"
		return token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	}()
	if err != nil {
		t.Fatal(err)
	}
	// The classic confusion: an HMAC signed with the public key, which anyone can get.
	confused, err := func() (string, error) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, googleClaims(now))
		token.Header["kid"] = "one"
		return token.SignedString(key.N.Bytes())
	}()
	if err != nil {
		t.Fatal(err)
	}
	valid := signGoogle(t, key, "one", googleClaims(now))
	parts := strings.Split(valid, ".")
	otherPayload := strings.Split(signGoogle(t, key, "one", changed(func(c jwt.MapClaims) { c["sub"] = "someone-else" })), ".")[1]

	cases := map[string]string{
		"another key's signature":  signGoogle(t, attacker, "one", googleClaims(now)),
		"attacker's own key id":    signGoogle(t, attacker, "attacker", googleClaims(now)),
		"no key id":                signGoogle(t, key, "", googleClaims(now)),
		"alg none":                 noAlg,
		"HMAC with the public key": confused,
		"swapped payload":          parts[0] + "." + otherPayload + "." + parts[2],
		"no signature":             parts[0] + "." + parts[1] + ".",
		"expired":                  signGoogle(t, key, "one", changed(func(c jwt.MapClaims) { c["exp"] = now.Add(-time.Minute).Unix() })),
		"no expiry":                signGoogle(t, key, "one", changed(func(c jwt.MapClaims) { delete(c, "exp") })),
		"not valid yet":            signGoogle(t, key, "one", changed(func(c jwt.MapClaims) { c["nbf"] = now.Add(time.Hour).Unix() })),
		"another audience":         signGoogle(t, key, "one", changed(func(c jwt.MapClaims) { c["aud"] = "another-client" })),
		"no audience":              signGoogle(t, key, "one", changed(func(c jwt.MapClaims) { delete(c, "aud") })),
		"garbage":                  "not-a-token",
		"empty":                    "",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if identity, err := server.keySet(time.Now).validate(context.Background(), raw, "client-id"); err == nil {
				t.Fatalf("accepted: %#v", identity)
			}
		})
	}
	t.Run("no client configured", func(t *testing.T) {
		if _, err := server.keySet(time.Now).validate(context.Background(), valid, ""); err == nil {
			t.Fatal("accepted with no client id")
		}
	})
	t.Run("audience list that includes this client", func(t *testing.T) {
		raw := signGoogle(t, key, "one", changed(func(c jwt.MapClaims) { c["aud"] = []string{"client-id", "another-client"} }))
		identity, err := server.keySet(time.Now).validate(context.Background(), raw, "client-id")
		if err == nil && validGoogleIdentity(identity, "nonce", "client-id") {
			t.Fatal("a shared audience signed someone in")
		}
	})
}

func TestGoogleKeysRotateWithoutLettingForgeriesHammerGoogle(t *testing.T) {
	now := time.Now()
	clock := now
	first, second := testRSAKey(t), testRSAKey(t)
	server := newGoogleKeyServer(t, map[string]*rsa.PublicKey{"one": &first.PublicKey})
	keys := server.keySet(func() time.Time { return clock })
	ctx := context.Background()
	if _, err := keys.validate(ctx, signGoogle(t, first, "one", googleClaims(now)), "client-id"); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.validate(ctx, signGoogle(t, first, "one", googleClaims(now)), "client-id"); err != nil {
		t.Fatal(err)
	}
	if got := server.requests.Load(); got != 1 {
		t.Fatalf("known keys fetched %d times", got)
	}
	// Unknown ids in a burst stop at one fetch.
	server.keys = map[string]*rsa.PublicKey{"one": &first.PublicKey, "two": &second.PublicKey}
	clock = clock.Add(2 * googleKeysRetry)
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := keys.validate(ctx, signGoogle(t, second, "forged", googleClaims(clock)), "client-id"); err == nil {
			t.Fatal("unknown key accepted")
		}
	}
	if got := server.requests.Load(); got != 2 {
		t.Fatalf("fetches after a burst = %d", got)
	}
	// That fetch learned the rotated key.
	if _, err := keys.validate(ctx, signGoogle(t, second, "two", googleClaims(clock)), "client-id"); err != nil {
		t.Fatal(err)
	}
	// Past their age, keys that can't be refreshed aren't trusted.
	server.status.Store(http.StatusServiceUnavailable)
	clock = clock.Add(2 * time.Hour)
	if _, err := keys.validate(ctx, signGoogle(t, first, "one", googleClaims(clock)), "client-id"); err == nil {
		t.Fatal("stale keys trusted while Google is unreachable")
	}
}

func TestGoogleKeyListRefusesWeakOrUnsuitableKeys(t *testing.T) {
	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(key *rsa.PublicKey) (string, string) {
		return base64.RawURLEncoding.EncodeToString(key.N.Bytes()), base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
	}
	n, e := encode(&small.PublicKey)
	if _, err := googlePublicKey(n, e); err == nil {
		t.Fatal("1024-bit key accepted")
	}
	n, _ = encode(&testRSAKey(t).PublicKey)
	if _, err := googlePublicKey(n, base64.RawURLEncoding.EncodeToString([]byte{1})); err == nil {
		t.Fatal("exponent 1 accepted")
	}
	if _, err := googlePublicKey("***", "AQAB"); err == nil {
		t.Fatal("bad encoding accepted")
	}
}
