package security

const sessionPublicIDPrefix = "ses_"

func SessionPublicIDFromHash(tokenHash string) string {
	return sessionPublicIDPrefix + HashToken("misty-session-id:" + tokenHash)[:32]
}

func SessionPublicID(sessionID string) string {
	return SessionPublicIDFromHash(HashToken(sessionID))
}
