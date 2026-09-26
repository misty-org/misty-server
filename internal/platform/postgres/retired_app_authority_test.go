package db

import (
	"errors"
	"testing"
)

func TestRetiredAppAuthorityCannotBecomeAccountAuthority(t *testing.T) {
	database := &Database{}
	for _, principal := range []AppExecutionAuthority{
		{},
		{Generation: 1, UserID: "owner", AppID: "retired", Scopes: []string{"ai.write", "capabilities.invoke"}},
	} {
		if err := database.ValidateAppExecutionAuthority(t.Context(), &principal, "owner", "", "ai.write"); !errors.Is(err, ErrAppRuntimeForbidden) {
			t.Fatalf("retired principal accepted: %v", err)
		}
	}
	if err := database.ValidateAppExecutionAuthority(t.Context(), nil, "owner", ""); err != nil {
		t.Fatal("ordinary account required an app grant", err)
	}
}
