package api

import "testing"

func TestSocialProviderUpgradeRequiresReviewAndIssuesCurrentScopes(t *testing.T) {
	testOfficialAppPermissionUpgrade(t, "chat", 3, []string{"messages.read", "messages.write"})
}

func TestInboxProviderUpgradeRequiresReviewAndIssuesCurrentScopes(t *testing.T) {
	testOfficialAppPermissionUpgrade(t, "inbox", 3, []string{"mail.read", "mail.write", "storage.read", "storage.write"})
}
