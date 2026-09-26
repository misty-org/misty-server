package accounts

import (
	"context"
	"database/sql"
	"strings"
)

// Store owns account persistence and its RLS scopes. The shared PostgreSQL
// adapter supplies transaction handling and legacy identity initialization;
// neither knows account registration, profile, or session behavior.
type Store struct {
	Transaction    func(context.Context, map[string]string, func(*sql.Tx) error) error
	CreateIdentity func(*sql.Tx, string, string) error
}

func AnonymousScope(email string) map[string]string {
	return map[string]string{"app.rls_mode": "anonymous", "app.current_email": NormalizeEmail(email)}
}
func RegistrationScope(user, identity, email string) map[string]string {
	return map[string]string{"app.rls_mode": "registration", "app.current_user_id": strings.TrimSpace(user), "app.current_license_id": strings.TrimSpace(identity), "app.current_email": NormalizeEmail(email)}
}
func UserScope(user string) map[string]string {
	return map[string]string{"app.rls_mode": "user", "app.current_user_id": strings.TrimSpace(user)}
}
func SessionScope(hash string) map[string]string {
	return map[string]string{"app.rls_mode": "session", "app.current_session_token_hash": strings.TrimSpace(hash)}
}
func SessionCreateScope(hash, user string) map[string]string {
	settings := SessionScope(hash)
	settings["app.current_user_id"] = strings.TrimSpace(user)
	return settings
}
