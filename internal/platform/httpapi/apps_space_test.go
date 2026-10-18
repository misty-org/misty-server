package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/integrations/composio"
)

func testAccount(t *testing.T, id, toolkit, status string) composio.Account {
	t.Helper()
	var account composio.Account
	if err := json.Unmarshal([]byte(`{"id":"`+id+`","status":"`+status+`","toolkit":{"slug":"`+toolkit+`"}}`), &account); err != nil {
		t.Fatal(err)
	}
	return account
}

func TestRunAppsUseTheirOwnActiveAccounts(t *testing.T) {
	accounts := []composio.Account{
		testAccount(t, "ca_work", "gmail", "ACTIVE"),
		testAccount(t, "ca_slack", "slack", "ACTIVE"),
		testAccount(t, "ca_broken", "github", "EXPIRED"),
	}
	apps := newRunApps(accounts)

	cases := []struct {
		name               string
		toolkit, requested string
		account            string
		need               appsNeed
	}{
		{"a single account is used without naming it", "gmail", "", "ca_work", appsReady},
		{"slack is available in every Space", "slack", "", "ca_slack", appsReady},
		{"a named account of theirs", "gmail", "ca_work", "ca_work", appsReady},
		{"an account that isn't theirs", "gmail", "ca_other", "", appsNeedOther},
		{"a broken account counts as none", "github", "", "", appsNeedConnect},
		{"an app never connected asks to connect", "notion", "", "", appsNeedConnect},
	}
	for _, tc := range cases {
		account, need := apps.account(tc.toolkit, tc.requested)
		if account != tc.account || need != tc.need {
			t.Errorf("%s: got %q/%q, want %q/%q", tc.name, account, need, tc.account, tc.need)
		}
	}

	both := newRunApps(append(accounts, testAccount(t, "ca_home", "gmail", "ACTIVE")))
	if _, need := both.account("gmail", ""); need != appsNeedChoose {
		t.Fatalf("two accounts without a choice = %q", need)
	}
	if account, need := both.account("gmail", "ca_home"); account != "ca_home" || need != appsReady {
		t.Fatalf("named account = %q/%q", account, need)
	}
	if err := appsNeedError(both, "gmail", appsNeedChoose); err == nil || !strings.Contains(err.Error(), "ca_work") || !strings.Contains(err.Error(), "ca_home") {
		t.Fatalf("choose message = %v", err)
	}
}

func TestAppsSearchViewShowsTheirAccounts(t *testing.T) {
	var result composio.SearchResult
	if err := json.Unmarshal([]byte(`{"success":true,"toolkit_connection_statuses":[
		{"toolkit":"gmail","has_active_connection":true,"accounts":[{"id":"ca_work","status":"ACTIVE"}]},
		{"toolkit":"slack","has_active_connection":false,"accounts":[]}]}`), &result); err != nil {
		t.Fatal(err)
	}
	mine := newRunApps([]composio.Account{testAccount(t, "ca_work", "gmail", "ACTIVE")})
	encoded, _ := json.Marshal(appsSearchView(result, searchAppPolicy{}, mine))
	if text := string(encoded); !strings.Contains(text, "ca_work") {
		t.Fatalf("search dropped the person's account: %s", text)
	}
}
