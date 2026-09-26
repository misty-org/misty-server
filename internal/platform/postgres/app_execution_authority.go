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

// Retired app credentials never become account credentials. Keep this explicit
// denial for persisted runs carrying an old principal during recovery.
func (db *Database) ValidateAppExecutionAuthority(ctx context.Context, authority *AppExecutionAuthority, userID, spaceID string, scopes ...string) error {
	if authority != nil {
		return ErrAppRuntimeForbidden
	}
	return nil
}
func validateAppExecutionAuthorityTx(ctx context.Context, tx *sql.Tx, authority *AppExecutionAuthority, userID, spaceID string, scopes ...string) error {
	if authority != nil {
		return ErrAppRuntimeForbidden
	}
	return nil
}
