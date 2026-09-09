package capabilities

import (
	"crypto/sha256"
	"github.com/google/uuid"
)

// AgentSDKIdentities preserves the existing journal identity across callers,
// approval waits, routine recovery and transport retries.
func AgentSDKIdentities(userID, runID, callID string) (requestID, effectID string) {
	identity := userID + "\x00" + runID + "\x00" + callID
	return uuid.NewHash(sha256.New(), uuid.NameSpaceOID, []byte("misty.sdk.request\x00"+identity), 5).String(), uuid.NewHash(sha256.New(), uuid.NameSpaceOID, []byte("misty.sdk.effect\x00"+identity), 5).String()
}
