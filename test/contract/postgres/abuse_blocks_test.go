package db

import (
	"context"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAbuseBlockSurvivesAndExpires(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()

	until := time.Now().UTC().Add(10 * time.Minute)
	if err := database.SaveAbuseBlock(ctx, AbuseBlock{
		Key: "ip:203.0.113.7", BlockedUntil: until, BlockSeconds: 600, Reason: "rate_limit_abuse",
	}); err != nil {
		t.Fatalf("SaveAbuseBlock() error = %v", err)
	}

	// A fresh process reads the block back, so a restart does not forgive it.
	blocks, err := database.ActiveAbuseBlocks(ctx)
	if err != nil {
		t.Fatalf("ActiveAbuseBlocks() error = %v", err)
	}
	found := false
	for _, block := range blocks {
		if block.Key == "ip:203.0.113.7" {
			found = true
			if block.BlockSeconds != 600 {
				t.Fatalf("BlockSeconds = %d, want 600", block.BlockSeconds)
			}
		}
	}
	if !found {
		t.Fatal("a live block was not returned")
	}

	// An elapsed block must stop applying.
	if err := database.SaveAbuseBlock(ctx, AbuseBlock{
		Key: "ip:198.51.100.9", BlockedUntil: time.Now().UTC().Add(-time.Minute), BlockSeconds: 60,
	}); err != nil {
		t.Fatalf("SaveAbuseBlock(expired) error = %v", err)
	}
	blocks, _ = database.ActiveAbuseBlocks(ctx)
	for _, block := range blocks {
		if block.Key == "ip:198.51.100.9" {
			t.Fatal("an expired block was still reported as active")
		}
	}
}

func TestAbuseBlockEscalationIsNotLostOnRepeat(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	key := "acct:escalation-test"

	long := time.Now().UTC().Add(30 * time.Minute)
	if err := database.SaveAbuseBlock(ctx, AbuseBlock{Key: key, BlockedUntil: long, BlockSeconds: 1800}); err != nil {
		t.Fatalf("SaveAbuseBlock() error = %v", err)
	}
	// A later, shorter block must not shorten an existing longer one — that
	// would let an offender downgrade their own penalty.
	short := time.Now().UTC().Add(time.Minute)
	if err := database.SaveAbuseBlock(ctx, AbuseBlock{Key: key, BlockedUntil: short, BlockSeconds: 60}); err != nil {
		t.Fatalf("SaveAbuseBlock(short) error = %v", err)
	}

	blocks, _ := database.ActiveAbuseBlocks(ctx)
	for _, block := range blocks {
		if block.Key == key {
			if block.BlockSeconds != 1800 {
				t.Fatalf("BlockSeconds = %d, want the longer 1800 retained", block.BlockSeconds)
			}
			if block.BlockedUntil.Before(long.Add(-time.Second)) {
				t.Fatal("the longer expiry was shortened by a later, weaker block")
			}
			return
		}
	}
	t.Fatal("block not found")
}

func TestClearAbuseBlockLiftsIt(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	key := "ip:192.0.2.55"

	_ = database.SaveAbuseBlock(ctx, AbuseBlock{
		Key: key, BlockedUntil: time.Now().UTC().Add(time.Hour), BlockSeconds: 3600,
	})
	if err := database.ClearAbuseBlock(ctx, key); err != nil {
		t.Fatalf("ClearAbuseBlock() error = %v", err)
	}
	blocks, _ := database.ActiveAbuseBlocks(ctx)
	for _, block := range blocks {
		if block.Key == key {
			t.Fatal("a cleared block is still active")
		}
	}
}

func TestSaveAbuseBlockRejectsInvalidInput(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	if err := database.SaveAbuseBlock(ctx, AbuseBlock{Key: "", BlockSeconds: 60}); err == nil {
		t.Fatal("an empty key was accepted")
	}
	if err := database.SaveAbuseBlock(ctx, AbuseBlock{Key: "ip:1.2.3.4", BlockSeconds: 0}); err == nil {
		t.Fatal("a zero duration was accepted")
	}
}
