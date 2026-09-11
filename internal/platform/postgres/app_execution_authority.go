package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type appExecutionContextKey struct{}

// AppExecutionAuthority is a persisted ceiling, never a credential or a grant.
type AppExecutionAuthority struct {
	Generation int64    `json:"installation_generation"`
	AppID      string   `json:"app_id"`
	UserID     string   `json:"user_id"`
	SpaceID    string   `json:"space_id,omitempty"`
	Scopes     []string `json:"scopes"`
}

func WithAppExecutionAuthority(ctx context.Context, session AppRuntimeSession) context.Context {
	return context.WithValue(ctx, appExecutionContextKey{}, AppExecutionAuthority{Generation: session.AuthorityGeneration, AppID: session.AppID, UserID: session.UserID, SpaceID: session.SpaceID, Scopes: append([]string{}, session.Scopes...)})
}

func AppAuthorityFromContext(ctx context.Context) *AppExecutionAuthority {
	value, ok := ctx.Value(appExecutionContextKey{}).(AppExecutionAuthority)
	if !ok {
		return nil
	}
	value.Scopes = append([]string{}, value.Scopes...)
	return &value
}

func AppAuthorityFromPayload(payload json.RawMessage) (*AppExecutionAuthority, error) {
	var value struct {
		Authority *AppExecutionAuthority `json:"_misty_authority"`
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, ErrSpaceInvalid
	}
	if value.Authority != nil && (value.Authority.UserID == "" || value.Authority.AppID == "") {
		return nil, ErrAppRuntimeForbidden
	}
	return value.Authority, nil
}

func (db *Database) ExecutionAuthorityForRun(ctx context.Context, runID, userID string) (*AppExecutionAuthority, error) {
	if authority := AppAuthorityFromContext(ctx); authority != nil {
		return authority, nil
	}
	if runID == "" {
		return nil, nil
	}
	var payload json.RawMessage
	query := `SELECT input FROM space_runs WHERE id=$1 AND owner_user_id=$2`
	if strings.HasPrefix(runID, "invocation_") {
		query = `SELECT request_payload FROM ai_invocations WHERE id=$1 AND user_id=$2`
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error { return tx.QueryRowContext(ctx, query, runID, userID).Scan(&payload) })
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAppRuntimeForbidden
	}
	if err != nil {
		return nil, err
	}
	return AppAuthorityFromPayload(payload)
}

func ContextWithPersistedAppAuthority(ctx context.Context, payload json.RawMessage) (context.Context, error) {
	authority, err := AppAuthorityFromPayload(payload)
	if err != nil {
		return ctx, err
	}
	if authority == nil {
		return ctx, nil
	}
	return WithAppExecutionAuthority(ctx, AppRuntimeSession{AuthorityGeneration: authority.Generation, UserID: authority.UserID, AppID: authority.AppID, SpaceID: authority.SpaceID, Scopes: authority.Scopes}), nil
}

// Replace any caller-supplied authority before persisting an admission.
func bindAppAuthority(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
	var value map[string]json.RawMessage
	if json.Unmarshal(payload, &value) != nil || value == nil {
		return nil, ErrSpaceInvalid
	}
	delete(value, "_misty_authority")
	if authority := AppAuthorityFromContext(ctx); authority != nil {
		encoded, err := json.Marshal(authority)
		if err != nil {
			return nil, err
		}
		value["_misty_authority"] = encoded
	}
	return json.Marshal(value)
}

// Validate current installation grants as well as the admission-time ceiling.
// The short-lived app bearer may expire while a durable run is waiting.
func (db *Database) ValidateAppExecutionAuthority(ctx context.Context, authority *AppExecutionAuthority, userID, spaceID string, scopes ...string) error {
	if authority == nil {
		return nil
	}
	// Reject malformed principals before opening a connection, as before.
	if authority.Generation <= 0 || authority.UserID != userID || (authority.SpaceID == "" || authority.SpaceID != spaceID) {
		return ErrAppRuntimeForbidden
	}
	for _, scope := range scopes {
		found := false
		for _, granted := range authority.Scopes {
			if scope != "" && scope == granted {
				found = true
			}
		}
		if !found {
			return ErrAppRuntimeForbidden
		}
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return validateAppExecutionAuthorityTx(ctx, tx, authority, userID, spaceID, scopes...)
	})
}

func validateAppExecutionAuthorityTx(ctx context.Context, tx *sql.Tx, authority *AppExecutionAuthority, userID, spaceID string, scopes ...string) error {
	if authority == nil {
		return nil
	}
	if authority.Generation <= 0 || authority.UserID != userID || (authority.SpaceID == "" || authority.SpaceID != spaceID) {
		return ErrAppRuntimeForbidden
	}
	contains := func(values []string, key string) bool {
		for _, value := range values {
			if value == key {
				return true
			}
		}
		return false
	}
	for _, scope := range scopes {
		if scope == "" || !contains(authority.Scopes, scope) {
			return ErrAppRuntimeForbidden
		}
	}
	if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
		return ErrAppRuntimeForbidden
	}
	var raw []byte
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT granted_scopes,authority_generation FROM space_app_installations WHERE space_id=$1 AND app_id=$2 AND state='installed' FOR SHARE`, spaceID, authority.AppID).Scan(&raw, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAppRuntimeForbidden
	}
	if err != nil {
		return err
	}
	if generation != authority.Generation {
		return ErrAppRuntimeForbidden
	}
	var current []string
	if json.Unmarshal(raw, &current) != nil {
		return ErrAppRuntimeForbidden
	}
	for _, scope := range scopes {
		if !contains(current, scope) {
			return ErrAppRuntimeForbidden
		}
	}
	return nil
}
