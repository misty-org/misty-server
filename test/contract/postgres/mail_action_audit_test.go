package db

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestMailActionAuditIsContentFreeAndOwnerScoped(t *testing.T) {
	database := openTestDatabase(t)
	owner, err := database.CreateUser("Audit Owner", "mail-audit-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	connection, err := database.SaveConnectedAccount(t.Context(), ConnectedAccount{UserID: owner.ID,
		Provider: "google", AccountID: "audit-account", AccountDisplay: "owner@example.com",
		CredentialCiphertext: []byte("sealed"), CredentialNonce: []byte("nonce"), KeyVersion: 1,
		Capabilities: []string{"mail"}})
	if err != nil {
		t.Fatal(err)
	}
	err = database.RecordMailActionAudit(t.Context(), MailActionAudit{UserID: owner.ID,
		ConnectionID: connection.ID, Action: "draft_send", TargetType: "draft", TargetID: "draft-1",
		Source: "ai", Confirmed: true, Success: true})
	if err != nil {
		t.Fatalf("RecordMailActionAudit() error = %v", err)
	}
	for _, forbidden := range []string{"body", "recipient", "subject", "attachment"} {
		var count int
		if err := database.Conn.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
			WHERE table_name='mail_action_audit' AND column_name LIKE '%' || $1 || '%'`, forbidden).Scan(&count); err != nil || count != 0 {
			t.Fatalf("forbidden %q columns = %d, err=%v", forbidden, count, err)
		}
	}
}
