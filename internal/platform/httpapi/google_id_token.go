package api

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	googleKeysURL = "https://www.googleapis.com/oauth2/v3/certs"
	// Keys are refetched at least this often, whatever Google's headers say.
	googleKeysMaxAge = 24 * time.Hour
	// An unknown key id can't make the server call Google more often than this.
	googleKeysRetry = time.Minute
	googleKeysLimit = 1 << 20
	googleMinKeyLen = 2048
)

var googleMaxAge = regexp.MustCompile(`(?i)\bmax-age=(\d+)`)

// googleIdentity is what a Google ID token says, once its signature, audience and
// expiry have been checked. validGoogleIdentity decides whether it's enough to sign in.
type googleIdentity struct {
	Issuer   string
	Audience string
	Subject  string
	Expires  int64
	Claims   map[string]any
}

// googleKeySet holds Google's signing keys. A key that can't be fetched fails the
// sign-in; nothing falls back to trusting the token.
type googleKeySet struct {
	url    string
	client *http.Client
	now    func() time.Time

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	expires time.Time
	fetched time.Time
}

func newGoogleKeySet() *googleKeySet {
	return &googleKeySet{url: googleKeysURL, client: &http.Client{Timeout: 10 * time.Second}, now: time.Now}
}

func (k *googleKeySet) key(ctx context.Context, id string) (*rsa.PublicKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := k.now()
	fresh := now.Before(k.expires)
	if key := k.keys[id]; key != nil && fresh {
		return key, nil
	}
	// Google rotates keys, so an unknown id can mean a new key. Forged tokens can name any
	// id, so that alone only asks Google once in a while.
	if fresh && now.Sub(k.fetched) < googleKeysRetry {
		return nil, errors.New("unknown Google signing key")
	}
	keys, maxAge, err := k.fetch(ctx)
	if err != nil {
		return nil, err
	}
	k.keys, k.fetched, k.expires = keys, now, now.Add(maxAge)
	if key := keys[id]; key != nil {
		return key, nil
	}
	return nil, errors.New("unknown Google signing key")
}

func (k *googleKeySet) fetch(ctx context.Context) (map[string]*rsa.PublicKey, time.Duration, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url, nil)
	if err != nil {
		return nil, 0, err
	}
	response, err := k.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("Google signing keys: status %d", response.StatusCode)
	}
	var body struct {
		Keys []struct {
			KeyID   string `json:"kid"`
			Type    string `json:"kty"`
			Use     string `json:"use"`
			Alg     string `json:"alg"`
			Modulus string `json:"n"`
			Expo    string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, googleKeysLimit)).Decode(&body); err != nil {
		return nil, 0, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, entry := range body.Keys {
		if entry.KeyID == "" || entry.Type != "RSA" || (entry.Use != "" && entry.Use != "sig") || (entry.Alg != "" && entry.Alg != "RS256") {
			continue
		}
		if key, err := googlePublicKey(entry.Modulus, entry.Expo); err == nil {
			keys[entry.KeyID] = key
		}
	}
	if len(keys) == 0 {
		return nil, 0, errors.New("Google signing keys: none usable")
	}
	maxAge := time.Hour
	if match := googleMaxAge.FindStringSubmatch(response.Header.Get("Cache-Control")); match != nil {
		if seconds, err := strconv.Atoi(match[1]); err == nil && seconds > 0 {
			maxAge = time.Duration(seconds) * time.Second
		}
	}
	return keys, min(maxAge, googleKeysMaxAge), nil
}

func googlePublicKey(modulus, exponent string) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(modulus)
	if err != nil {
		return nil, err
	}
	e, err := base64.RawURLEncoding.DecodeString(exponent)
	if err != nil {
		return nil, err
	}
	key := &rsa.PublicKey{N: new(big.Int).SetBytes(n)}
	value := new(big.Int).SetBytes(e)
	if !value.IsInt64() || value.Int64() < 3 || value.Int64() > 1<<31-1 || key.N.BitLen() < googleMinKeyLen {
		return nil, errors.New("unsuitable Google signing key")
	}
	key.E = int(value.Int64())
	return key, nil
}

// validate checks a token's signature against Google's keys, its algorithm, audience and
// expiry. Only RS256 is accepted, so a token can't choose how it's checked.
func (k *googleKeySet) validate(ctx context.Context, raw, audience string) (*googleIdentity, error) {
	if audience == "" {
		return nil, errors.New("no Google client id")
	}
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithAudience(audience),
		jwt.WithTimeFunc(k.now),
	)
	if _, err := parser.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		id, _ := token.Header["kid"].(string)
		if id == "" {
			return nil, errors.New("Google token names no signing key")
		}
		return k.key(ctx, id)
	}); err != nil {
		return nil, err
	}
	// Google issues a single audience; a list could name someone else as well.
	single, _ := claims["aud"].(string)
	issuer, _ := claims["iss"].(string)
	subject, _ := claims["sub"].(string)
	expires, err := claims.GetExpirationTime()
	if err != nil || expires == nil {
		return nil, errors.New("Google token has no expiry")
	}
	return &googleIdentity{Issuer: issuer, Audience: single, Subject: subject, Expires: expires.Unix(), Claims: claims}, nil
}
