package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/misty-org/misty-server/internal/platform/config"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// An account proof says, for a few minutes, that a signed-in person holds this
// account. They carry it to a service the operator runs, which can't see their
// session; the service checks it through the operator account API, so it learns who
// they are without ever holding their password or session.
const accountProofTTL = 10 * time.Minute

var errAccountProofInvalid = errors.New("account_proof_invalid")

func accountProofMAC(payload string) ([]byte, error) {
	key := strings.TrimSpace(config.Getenv("MISTY_AUTH_SIGNING_KEY"))
	if key == "" {
		return nil, errors.New("MISTY_AUTH_SIGNING_KEY is unset")
	}
	// The purpose keeps a proof from ever passing for anything else signed with this key.
	mac := hmac.New(sha256.New, []byte("misty.account-proof.v1\x00"+key))
	mac.Write([]byte(payload))
	return mac.Sum(nil), nil
}

func newAccountProof(userID string, now time.Time) (string, error) {
	payload := userID + "." + strconv.FormatInt(now.Add(accountProofTTL).Unix(), 10)
	sum, err := accountProofMAC(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(sum), nil
}

// accountFromProof is the account a proof names, if it is genuine and current.
func accountFromProof(proof string, now time.Time) (string, error) {
	encoded, signature, ok := strings.Cut(strings.TrimSpace(proof), ".")
	if !ok {
		return "", errAccountProofInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", errAccountProofInvalid
	}
	given, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return "", errAccountProofInvalid
	}
	want, err := accountProofMAC(string(raw))
	if err != nil {
		return "", err
	}
	if !hmac.Equal(given, want) {
		return "", errAccountProofInvalid
	}
	dot := strings.LastIndex(string(raw), ".")
	if dot <= 0 {
		return "", errAccountProofInvalid
	}
	expires, err := strconv.ParseInt(string(raw)[dot+1:], 10, 64)
	if err != nil || now.Unix() >= expires {
		return "", errAccountProofInvalid
	}
	return string(raw)[:dot], nil
}

// AccountProof answers POST /me/account-proof with a proof of the signed-in account.
func AccountProof(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		proof, err := newAccountProof(userID, time.Now())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"proof": proof})
	}
}
