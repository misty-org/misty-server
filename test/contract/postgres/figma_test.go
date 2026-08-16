package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/google/uuid"
)

type figmaPostgresFixture struct {
	database   *Database
	owner      *User
	member     *User
	space      *Space
	connection *ConnectedAccount
	binding    *FigmaBinding
}

func setupFigmaPostgres(t *testing.T) figmaPostgresFixture {
	t.Helper()
	database := openTestDatabase(t)
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString()[:12], "-", "")
	owner, err := database.CreateUserWithUsername("Figma DB Owner", "fig_owner_"+suffix, "fig-owner-"+suffix+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUserWithUsername("Figma DB Member", "fig_member_"+suffix, "fig-member-"+suffix+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Figma DB")
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	connection, err := database.SaveConnectedAccount(ctx, ConnectedAccount{
		UserID: owner.ID, Provider: "figma", AccountID: "figma-" + suffix,
		AccountDisplay: "designer-" + suffix, CredentialCiphertext: []byte("encrypted"),
		CredentialNonce: []byte("nonce"), KeyVersion: 1,
		Capabilities: []string{"drawings_read", "drawings_comments", "drawings_webhooks"},
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := database.CreateFigmaBinding(ctx, owner.ID, space.ID, connection.ID, FigmaBinding{
		ResourceType: "file", ExternalID: "file-" + suffix, DisplayName: "Launch canvas",
	})
	if err != nil {
		t.Fatal(err)
	}
	return figmaPostgresFixture{database: database, owner: owner, member: member, space: space, connection: connection, binding: binding}
}

func TestFigmaBindingOwnershipAndRLSSeparateMemberReadsFromWrites(t *testing.T) {
	fixture := setupFigmaPostgres(t)
	ctx := context.Background()

	bindings, err := fixture.database.FigmaBindings(ctx, fixture.member.ID, fixture.space.ID)
	if err != nil || len(bindings) != 1 || bindings[0].ID != fixture.binding.ID {
		t.Fatalf("member bindings=%#v err=%v", bindings, err)
	}
	if _, err := fixture.database.CreateFigmaBinding(ctx, fixture.member.ID, fixture.space.ID, fixture.connection.ID, FigmaBinding{
		ResourceType: "file", ExternalID: "member-file", DisplayName: "Member file",
	}); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("member CreateFigmaBinding error=%v, want ErrSpaceForbidden", err)
	}

	var selected int
	err = fixture.database.TestingWithRLSContext(ctx, map[string]string{
		"app.rls_mode": "user", "app.current_user_id": fixture.member.ID,
	}, func(tx *sql.Tx) error {
		var appRoleExists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app')`).Scan(&appRoleExists); err != nil {
			return err
		}
		if appRoleExists {
			if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE misty_app`); err != nil {
				return err
			}
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM figma_space_bindings WHERE id=$1`, fixture.binding.ID).Scan(&selected); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE figma_space_bindings SET display_name='tampered' WHERE id=$1`, fixture.binding.ID)
		if err != nil {
			return err
		}
		changed, _ := result.RowsAffected()
		if changed != 0 {
			t.Fatalf("member updated %d Figma bindings through RLS", changed)
		}
		return nil
	})
	if err != nil || selected != 1 {
		t.Fatalf("member RLS selected=%d err=%v", selected, err)
	}
}

func TestFigmaCommentClaimsAreIdempotentAndAudited(t *testing.T) {
	fixture := setupFigmaPostgres(t)
	ctx := context.Background()
	fingerprint := strings.Repeat("a", 64)
	claimed, err := fixture.database.ClaimFigmaCommentAction(ctx, fixture.owner.ID, fixture.space.ID,
		fixture.binding.ID, "user", fixture.binding.FileKey, "1:2", "comment-action-1", fingerprint)
	if err != nil || !claimed {
		t.Fatalf("first claim=%v err=%v", claimed, err)
	}
	claimed, err = fixture.database.ClaimFigmaCommentAction(ctx, fixture.owner.ID, fixture.space.ID,
		fixture.binding.ID, "user", fixture.binding.FileKey, "1:2", "comment-action-1", fingerprint)
	if err != nil || claimed {
		t.Fatalf("duplicate claim=%v err=%v", claimed, err)
	}
	if err := fixture.database.FinishFigmaCommentAction(ctx, fixture.binding.ID, "comment-action-1", "", true); err != nil {
		t.Fatal(err)
	}
	var count int
	err = fixture.database.Conn.QueryRow(`SELECT COUNT(*) FROM figma_comment_audit
		WHERE binding_id=$1 AND idempotency_key='comment-action-1' AND action_fingerprint=$2
		AND confirmed=TRUE AND success=TRUE AND error_code=''`, fixture.binding.ID, fingerprint).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("comment audit count=%d err=%v", count, err)
	}
}

func TestFigmaWebhookDeliveryReclaimsFailedAndStaleProcessingClaims(t *testing.T) {
	fixture := setupFigmaPostgres(t)
	ctx := context.Background()
	subscription, err := fixture.database.SaveFigmaWebhookSubscription(ctx, fixture.binding.ID,
		"webhook-1", "FILE_UPDATE", strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}

	failedHash := strings.Repeat("c", 64)
	claimed, err := fixture.database.BeginFigmaWebhookDelivery(ctx, failedHash, subscription.ID,
		subscription.WebhookID, subscription.EventType, fixture.binding.FileKey, nil)
	if err != nil || !claimed {
		t.Fatalf("first failed claim=%v err=%v", claimed, err)
	}
	if err := fixture.database.FinishFigmaWebhookDelivery(ctx, failedHash, "failed", "record_upsert_failed"); err != nil {
		t.Fatal(err)
	}
	claimed, err = fixture.database.BeginFigmaWebhookDelivery(ctx, failedHash, subscription.ID,
		subscription.WebhookID, subscription.EventType, fixture.binding.FileKey, nil)
	if err != nil || !claimed {
		t.Fatalf("failed delivery reclaim=%v err=%v", claimed, err)
	}

	staleHash := strings.Repeat("d", 64)
	claimed, err = fixture.database.BeginFigmaWebhookDelivery(ctx, staleHash, subscription.ID,
		subscription.WebhookID, subscription.EventType, fixture.binding.FileKey, nil)
	if err != nil || !claimed {
		t.Fatalf("first stale claim=%v err=%v", claimed, err)
	}
	err = fixture.database.TestingWithRLSContext(ctx, map[string]string{"app.rls_mode": "service"}, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE figma_webhook_deliveries SET received_at=NOW()-INTERVAL '6 minutes' WHERE delivery_hash=$1`, staleHash)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = fixture.database.BeginFigmaWebhookDelivery(ctx, staleHash, subscription.ID,
		subscription.WebhookID, subscription.EventType, fixture.binding.FileKey, nil)
	if err != nil || !claimed {
		t.Fatalf("stale processing reclaim=%v err=%v", claimed, err)
	}

	if err := fixture.database.FinishFigmaWebhookDelivery(ctx, staleHash, "processed", ""); err != nil {
		t.Fatal(err)
	}
	claimed, err = fixture.database.BeginFigmaWebhookDelivery(ctx, staleHash, subscription.ID,
		subscription.WebhookID, subscription.EventType, fixture.binding.FileKey, nil)
	if err != nil || claimed {
		t.Fatalf("processed delivery reclaim=%v err=%v", claimed, err)
	}
}
