package accounts

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

var ErrInvalidUsername = errors.New("username must be 3-30 lowercase letters, numbers, or underscores")
var ErrInvalidPassword = errors.New("password must be at least 8 characters and at most 72 bytes")

// ValidateNewPassword is the rule for a password someone chooses: long enough
// to resist online guessing, and within bcrypt's 72-byte input limit. Existing
// passwords are never re-checked at sign-in.
func ValidateNewPassword(password string) error {
	if utf8.RuneCountInString(password) < 8 || len(password) > 72 {
		return ErrInvalidPassword
	}
	return nil
}

var ErrUsernameTaken = errors.New("username already taken")

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func NormalizeUsername(username string) (string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if len(username) < 3 || len(username) > 30 {
		return "", ErrInvalidUsername
	}
	for _, character := range username {
		if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_') {
			return "", ErrInvalidUsername
		}
	}
	return username, nil
}

func DefaultUsernameForEmail(email string) string {
	localPart, _, _ := strings.Cut(NormalizeEmail(email), "@")
	var username strings.Builder
	lastWasUnderscore := false
	for _, character := range localPart {
		allowed := (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_'
		if allowed {
			username.WriteRune(character)
			lastWasUnderscore = character == '_'
		} else if username.Len() > 0 && !lastWasUnderscore {
			username.WriteByte('_')
			lastWasUnderscore = true
		}
		if username.Len() == 30 {
			break
		}
	}
	result := strings.Trim(username.String(), "_")
	if len(result) < 3 {
		return "user_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	}
	return result
}
