package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	. "github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
	"github.com/misty-org/misty-server/test/testkit"
)

func limit(n int64) *int64 { return &n }

func TestOperatorAccountCreateIsIdempotentPerEmail(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	first, created, err := database.CreateOperatorAccount(ctx, "Cloud.Person@Example.com", AccountLimits{Devices: limit(5)})
	if err != nil || !created || first.Provider != "misty" {
		t.Fatalf("create = %+v %v %v", first, created, err)
	}
	again, created, err := database.CreateOperatorAccount(ctx, "cloud.person@example.com", AccountLimits{Devices: limit(2), StorageBytes: limit(50_000_000_000)})
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("second create = %+v %v %v", again, created, err)
	}
	limits, err := database.AccountLimitsFor(ctx, first.ID)
	if err != nil || *limits.Devices != 2 || *limits.StorageBytes != 50_000_000_000 {
		t.Fatalf("limits = %+v %v", limits, err)
	}
	// No password yet: password sign-in finds an empty hash that matches nothing.
	if _, hash, err := database.GetUserByEmail(first.Email); err != nil || hash != "" {
		t.Fatalf("hash = %q %v", hash, err)
	}
	// The first password is set without a current one; later ones need it.
	if err := database.ChangePassword(first.ID, "", "a-long-enough-password", ""); err != nil {
		t.Fatalf("first password: %v", err)
	}
	if err := database.ChangePassword(first.ID, "", "another-long-password", ""); !errors.Is(err, ErrWrongCurrentPassword) {
		t.Fatalf("second password without current = %v", err)
	}
	// A taken username gets a suffix instead of failing.
	other, created, err := database.CreateOperatorAccount(ctx, "cloud.person@another.example", AccountLimits{})
	if err != nil || !created || other.Username == first.Username {
		t.Fatalf("username collision = %+v %v %v", other, created, err)
	}
}

func TestSetupKeysAreSingleUseReplacedAndExpire(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	user, _, err := database.CreateOperatorAccount(ctx, "keys@example.com", AccountLimits{})
	if err != nil {
		t.Fatal(err)
	}
	old, current, expired := security.HashToken("old"), security.HashToken("current"), security.HashToken("expired")
	if err := database.IssueSetupKey(ctx, user.ID, old, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := database.IssueSetupKey(ctx, user.ID, current, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RedeemSetupKey(ctx, old); !errors.Is(err, ErrSetupKeyInvalid) {
		t.Fatalf("replaced key = %v", err)
	}
	state, _ := database.AccountSetupState(ctx, user.ID)
	if state.Done || !state.LiveKey {
		t.Fatalf("state before redeem = %+v", state)
	}
	redeemed, err := database.RedeemSetupKey(ctx, current)
	if err != nil || redeemed.ID != user.ID {
		t.Fatalf("redeem = %+v %v", redeemed, err)
	}
	if _, err := database.RedeemSetupKey(ctx, current); !errors.Is(err, ErrSetupKeyInvalid) {
		t.Fatalf("second redeem = %v", err)
	}
	if state, _ := database.AccountSetupState(ctx, user.ID); !state.Done {
		t.Fatalf("state after redeem = %+v", state)
	}
	if err := database.IssueSetupKey(ctx, user.ID, expired, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RedeemSetupKey(ctx, expired); !errors.Is(err, ErrSetupKeyInvalid) {
		t.Fatalf("expired key = %v", err)
	}
}

func TestSuspensionEndsSessionsKeepsDataAndRefusesKeys(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	user, _, err := database.CreateOperatorAccount(ctx, "suspend@example.com", AccountLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateRefreshSession(ctx, security.HashToken("sid"), security.HashToken("refresh"), user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	key := security.HashToken("suspended-key")
	if err := database.IssueSetupKey(ctx, user.ID, key, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := database.SuspendAccount(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if ids, _ := database.SuspendedAccounts(ctx); len(ids) != 1 || ids[0] != user.ID {
		t.Fatalf("suspended = %v", ids)
	}
	var sessions int
	_ = database.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, user.ID).Scan(&sessions)
	})
	if sessions != 0 {
		t.Fatalf("sessions after suspend = %d", sessions)
	}
	if _, err := database.RedeemSetupKey(ctx, key); !errors.Is(err, ErrAccountSuspended) {
		t.Fatalf("redeem while suspended = %v", err)
	}
	if err := database.ResumeAccount(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	// The key was not spent while the account was suspended.
	if _, err := database.RedeemSetupKey(ctx, key); err != nil {
		t.Fatalf("redeem after resume = %v", err)
	}
	if found, _ := database.GetUserByID(user.ID); found == nil {
		t.Fatal("suspension removed the account")
	}
	if err := database.SuspendAccount(ctx, "missing"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("suspend unknown = %v", err)
	}
}

func TestGoogleSignInRefusesAnEmailAnotherAccountHolds(t *testing.T) {
	database := testkit.OpenDatabase(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	email := "same." + suffix + "@example.com"
	if _, err := database.CreateUserWithUsername("pw_"+suffix, email, "the-password"); err != nil {
		t.Fatal(err)
	}
	if found, err := database.GoogleUser(strings.ToUpper(email), "subject-"+suffix); !errors.Is(err, ErrPasswordAccount) {
		t.Fatalf("found %+v, %v", found, err)
	}
	if _, hash, err := database.GetUserByEmail(email); err != nil || hash == "" {
		t.Fatalf("the password went: %q, %v", hash, err)
	}
	// One Google identity holds an email; another claiming it is refused.
	google := "google." + suffix + "@example.com"
	if _, err := database.GoogleUser(google, "first-"+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GoogleUser(google, "second-"+suffix); !errors.Is(err, ErrProviderConflict) {
		t.Fatalf("second identity: %v", err)
	}
}

func TestSetUsername(t *testing.T) {
	database := testkit.OpenDatabase(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	user, err := database.GoogleUser("rename."+suffix+"@example.com", "rename-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.CreateUserWithUsername("taken_"+suffix, "taken."+suffix+"@example.com", "the-password"); err != nil {
		t.Fatal(err)
	}
	if saved, err := database.SetUsername(user.ID, " New_"+suffix+" "); err != nil || saved != "new_"+suffix {
		t.Fatalf("saved %q, %v", saved, err)
	}
	if read, err := database.GetUserByID(user.ID); err != nil || read.Username != "new_"+suffix {
		t.Fatalf("read %+v, %v", read, err)
	}
	if _, err = database.SetUsername(user.ID, "taken_"+suffix); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("taken: %v", err)
	}
	if _, err = database.SetUsername(user.ID, "no spaces"); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("invalid: %v", err)
	}
}

func TestDeviceLimitRefusesTheNextNewDevice(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	user, _, err := database.CreateOperatorAccount(ctx, "devices@example.com", AccountLimits{Devices: limit(1)})
	if err != nil {
		t.Fatal(err)
	}
	register := func(name string) error {
		key := base64.RawURLEncoding.EncodeToString([]byte(uuid.NewString()[:32]))
		_, err := database.RegisterUnifiedDevice(ctx, user.ID, name, key, "endpoint-"+uuid.NewString(), "macos", "15", "1.0", "", nil)
		return err
	}
	if err := database.SetAccountLimits(ctx, user.ID, AccountLimits{Devices: limit(0)}); err != nil {
		t.Fatal(err)
	}
	if err := register("Studio"); !errors.Is(err, ErrNoDevicesAllowed) {
		t.Fatalf("no devices allowed = %v", err)
	}
	if err := database.SetAccountLimits(ctx, user.ID, AccountLimits{Devices: limit(1)}); err != nil {
		t.Fatal(err)
	}
	if err := register("Studio"); err != nil {
		t.Fatalf("first device = %v", err)
	}
	if err := register("Laptop"); !errors.Is(err, ErrDeviceLimit) {
		t.Fatalf("second device = %v", err)
	}
	if err := database.SetAccountLimits(ctx, user.ID, AccountLimits{}); err != nil {
		t.Fatal(err)
	}
	if err := register("Laptop"); err != nil {
		t.Fatalf("unlimited device = %v", err)
	}
	account, err := database.OperatorAccountByID(ctx, user.ID)
	if err != nil || account.Usage.Devices != 2 || account.Usage.StorageBytes == nil {
		t.Fatalf("overview = %+v %v", account, err)
	}
}

func TestStorageLimitCountsLibraryAndVault(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	user, _, err := database.CreateOperatorAccount(ctx, "storage@example.com", AccountLimits{StorageBytes: limit(1000)})
	if err != nil {
		t.Fatal(err)
	}
	room := func(adding int64) error {
		return database.SpaceTx(ctx, func(tx *sql.Tx) error { return RequireAccountStorageRoomTx(ctx, tx, user.ID, adding) })
	}
	if err := room(1000); err != nil {
		t.Fatalf("exactly the limit = %v", err)
	}
	if err := room(1001); !errors.Is(err, ErrLibraryQuota) {
		t.Fatalf("over the limit = %v", err)
	}
	// Deleting never needs room.
	if err := room(-50); err != nil {
		t.Fatalf("negative = %v", err)
	}
	if err := database.SetAccountLimits(ctx, user.ID, AccountLimits{}); err != nil {
		t.Fatal(err)
	}
	if err := room(1 << 40); err != nil {
		t.Fatalf("unlimited = %v", err)
	}
}

func TestInvitationOpensRegistrationForThatEmail(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	if invited, err := database.PendingAccountInvitation(ctx, "friend@example.com"); err != nil || invited {
		t.Fatalf("no invitation = %v %v", invited, err)
	}
	owner, err := database.CreateUserWithUsername("space_owner", "owner@example.com", "password-long-enough")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Team")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.InviteToSpaceWithToken(ctx, owner.ID, space.ID, "Friend@Example.com", security.HashToken("invite")); err != nil {
		t.Fatal(err)
	}
	if invited, err := database.PendingAccountInvitation(ctx, "friend@example.com"); err != nil || !invited {
		t.Fatalf("invitation = %v %v", invited, err)
	}
}
