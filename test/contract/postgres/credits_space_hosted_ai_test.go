package db

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSpaceHostedAIChargesInitiatingMemberAndSpaceButNotOwner(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("AI Space Owner", "ai-space-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetLicenseStateByID(owner.LicenseID, TierPro, LicenseStatusActive, nil); err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("AI Space Member", "ai-space-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Metered together")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	ownerWallet, err := database.GetOrCreateHostedAIWallet(owner.ID, TierPro, now)
	if err != nil {
		t.Fatal(err)
	}
	ownerRemaining := ownerWallet.WeeklyRemainingMicrousd
	reservation, _, err := database.ReserveHostedAIUsageForSpace(member.ID, space.ID, TierBasic, HostedAIMeterAgent, "member-space-request", 20_000, now)
	if err != nil {
		t.Fatal(err)
	}
	if reservation.UserID != member.ID || reservation.SpaceID != space.ID {
		t.Fatalf("reservation attribution = %#v", reservation)
	}
	if _, err := database.SettleHostedAIReservation(reservation.ID, "member-space-request:settle", HostedAIUsage{ChargeMicrousd: 7_000}); err != nil {
		t.Fatal(err)
	}
	memberWallet, err := database.GetOrCreateHostedAIWallet(member.ID, TierBasic, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	spaceWallet, err := database.GetOrCreateSpaceHostedAIWallet(space.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ownerWallet, err = database.GetOrCreateHostedAIWallet(owner.ID, TierPro, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if memberWallet.WeeklyRemainingMicrousd != BasicWeeklyAgentAllowance-7_000 {
		t.Fatalf("member wallet = %#v", memberWallet)
	}
	if spaceWallet.WeeklyRemainingMicrousd != ProWeeklyAgentAllowance-7_000 {
		t.Fatalf("space wallet = %#v", spaceWallet)
	}
	if ownerWallet.WeeklyRemainingMicrousd != ownerRemaining {
		t.Fatalf("owner personal wallet was charged: before=%d after=%d", ownerRemaining, ownerWallet.WeeklyRemainingMicrousd)
	}
	personalUsage, err := database.HostedAIUsageByMeter(member.ID, now.Add(-time.Minute))
	if err != nil || len(personalUsage) != 1 || personalUsage[0].ChargedMicrousd != 7_000 {
		t.Fatalf("personal usage = %#v, %v", personalUsage, err)
	}
	spaceUsage, err := database.SpaceHostedAIUsageByMeter(space.ID, now.Add(-time.Minute))
	if err != nil || len(spaceUsage) != 1 || spaceUsage[0].ChargedMicrousd != 7_000 {
		t.Fatalf("space usage = %#v, %v", spaceUsage, err)
	}
}

func TestSpaceHostedAIEnforcesOwnerPlanCapacityAcrossMembers(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Free AI Owner", "free-ai-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Free capacity")
	if err != nil {
		t.Fatal(err)
	}
	members := make([]*User, 0, 2)
	for index, email := range []string{"max-ai-one@example.com", "max-ai-two@example.com"} {
		member, createErr := database.CreateUser("Max AI Member", email, "password123")
		if createErr != nil {
			t.Fatal(createErr)
		}
		if setErr := database.SetLicenseStateByID(member.LicenseID, TierMax, LicenseStatusActive, nil); setErr != nil {
			t.Fatal(setErr)
		}
		invite, inviteErr := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
		if inviteErr != nil {
			t.Fatal(inviteErr)
		}
		if _, acceptErr := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); acceptErr != nil {
			t.Fatal(acceptErr)
		}
		_ = index
		members = append(members, member)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	reservation, _, err := database.ReserveHostedAIUsageForSpace(members[0].ID, space.ID, TierMax, HostedAIMeterAgent, "fill-free-space", BasicWeeklyAgentAllowance, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SettleHostedAIReservation(reservation.ID, "fill-free-space:settle", HostedAIUsage{ChargeMicrousd: BasicWeeklyAgentAllowance}); err != nil {
		t.Fatal(err)
	}
	_, _, err = database.ReserveHostedAIUsageForSpace(members[1].ID, space.ID, TierMax, HostedAIMeterAgent, "over-free-space", 1, now.Add(time.Minute))
	var limit HostedAILimitReachedError
	if !errors.As(err, &limit) || limit.Scope != "space" {
		t.Fatalf("Space limit error = %#v, %v", limit, err)
	}
	secondWallet, err := database.GetOrCreateHostedAIWallet(members[1].ID, TierMax, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if secondWallet.WeeklyRemainingMicrousd != MaxWeeklyAgentAllowance {
		t.Fatalf("blocked member was personally charged: %#v", secondWallet)
	}
}

func TestSpaceHostedAIRefundRestoresBothWallets(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Refund Space User", "refund-space-user@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, user.ID, "Refund both")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	reservation, _, err := database.ReserveHostedAIUsageForSpace(user.ID, space.ID, TierBasic, HostedAIMeterAgent, "refund-both", 20_000, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SettleHostedAIReservation(reservation.ID, "refund-both:settle", HostedAIUsage{ChargeMicrousd: 7_000}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RefundHostedAIReservation(reservation.ID, "refund-both:refund", "internal_test"); err != nil {
		t.Fatal(err)
	}
	personal, err := database.GetOrCreateHostedAIWallet(user.ID, TierBasic, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	spaceWallet, err := database.GetOrCreateSpaceHostedAIWallet(space.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if personal.WeeklyRemainingMicrousd != BasicWeeklyAgentAllowance || spaceWallet.WeeklyRemainingMicrousd != BasicWeeklyAgentAllowance {
		t.Fatalf("refund wallets: personal=%#v space=%#v", personal, spaceWallet)
	}
}

func TestSpaceHostedAIPersonalUsageAggregatesAcrossSpaces(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Multi Space AI User", "multi-space-ai@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	first, err := database.CreateSpace(ctx, user.ID, "AI one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreateSpace(ctx, user.ID, "AI two")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for index, item := range []struct {
		spaceID string
		charge  int64
	}{{first.ID, 5_000}, {second.ID, 7_000}} {
		key := "multi-space-" + string(rune('a'+index))
		reservation, _, reserveErr := database.ReserveHostedAIUsageForSpace(user.ID, item.spaceID, TierBasic, HostedAIMeterAgent, key, 20_000, now)
		if reserveErr != nil {
			t.Fatal(reserveErr)
		}
		if _, settleErr := database.SettleHostedAIReservation(reservation.ID, key+":settle", HostedAIUsage{ChargeMicrousd: item.charge}); settleErr != nil {
			t.Fatal(settleErr)
		}
	}
	personal, err := database.GetOrCreateHostedAIWallet(user.ID, TierBasic, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if personal.WeeklyRemainingMicrousd != BasicWeeklyAgentAllowance-12_000 {
		t.Fatalf("personal wallet did not aggregate Spaces: %#v", personal)
	}
	firstWallet, err := database.GetOrCreateSpaceHostedAIWallet(first.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	secondWallet, err := database.GetOrCreateSpaceHostedAIWallet(second.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if firstWallet.WeeklyRemainingMicrousd != BasicWeeklyAgentAllowance-5_000 || secondWallet.WeeklyRemainingMicrousd != BasicWeeklyAgentAllowance-7_000 {
		t.Fatalf("Space wallets = first %#v second %#v", firstWallet, secondWallet)
	}
}

func TestSpaceHostedAIWalletReclaimsEveryMembersStaleReservation(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Stale AI Owner", "stale-ai-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Stale AI Member", "stale-ai-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Stale reservations")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	reservations := []*HostedAIReservation{}
	for index, userID := range []string{owner.ID, member.ID} {
		reservation, _, reserveErr := database.ReserveHostedAIUsageForSpace(userID, space.ID, TierBasic, HostedAIMeterAgent, "stale-space-"+string(rune('a'+index)), 20_000, now)
		if reserveErr != nil {
			t.Fatal(reserveErr)
		}
		reservations = append(reservations, reservation)
	}
	for _, reservation := range reservations {
		if _, err := database.Conn.Exec(`UPDATE hosted_ai_reservations SET created_at=NOW()-INTERVAL '16 minutes' WHERE id=$1`, reservation.ID); err != nil {
			t.Fatal(err)
		}
	}
	spaceWallet, err := database.GetOrCreateSpaceHostedAIWallet(space.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if spaceWallet.ReservedMicrousd != 0 {
		t.Fatalf("Space stale reservation balance = %d", spaceWallet.ReservedMicrousd)
	}
	for _, userID := range []string{owner.ID, member.ID} {
		wallet, walletErr := database.GetOrCreateHostedAIWallet(userID, TierBasic, now)
		if walletErr != nil {
			t.Fatal(walletErr)
		}
		if wallet.ReservedMicrousd != 0 {
			t.Fatalf("personal stale reservation balance for %s = %d", userID, wallet.ReservedMicrousd)
		}
	}
}

func TestSpaceHostedAIWalletRefreshesDuringOwnershipTransfer(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Transfer AI Owner", "transfer-ai-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := database.CreateUser("Transfer AI Recipient", "transfer-ai-recipient@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetLicenseStateByID(recipient.LicenseID, TierPro, LicenseStatusActive, nil); err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Transfer allowance")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, recipient.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, recipient.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	reservation, _, err := database.ReserveHostedAIUsageForSpace(owner.ID, space.ID, TierBasic, HostedAIMeterAgent, "transfer-ai-consumption", 20_000, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SettleHostedAIReservation(reservation.ID, "transfer-ai-consumption:settle", HostedAIUsage{ChargeMicrousd: 7_000}); err != nil {
		t.Fatal(err)
	}
	if err := database.TransferSpaceOwnership(ctx, owner.ID, space.ID, recipient.ID); err != nil {
		t.Fatal(err)
	}
	var allowance, remaining int64
	if err := database.Conn.QueryRow(`SELECT weekly_allowance_microusd,weekly_remaining_microusd FROM space_hosted_ai_wallets WHERE space_id=$1`, space.ID).Scan(&allowance, &remaining); err != nil {
		t.Fatal(err)
	}
	if allowance != ProWeeklyAgentAllowance || remaining != ProWeeklyAgentAllowance-7_000 {
		t.Fatalf("transferred Space wallet allowance=%d remaining=%d", allowance, remaining)
	}
}
