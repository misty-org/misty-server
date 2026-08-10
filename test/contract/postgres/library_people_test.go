package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestLibraryPeoplePolicyAssignmentMergeAndIsolation(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("People Owner", "people-owner@example.com", "password123")
	member, _ := database.CreateUser("People Member", "people-member@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "People").ID
	item := createPeopleTestImage(t, database, owner.ID, spaceID, "family.jpg", "c")

	policy, err := database.LibraryPeoplePolicy(ctx, owner.ID, spaceID)
	if err != nil || policy.Version != 0 || policy.FacesEnabled || policy.PetsEnabled {
		t.Fatalf("initial policy = %#v, %v", policy, err)
	}
	policy, err = database.UpdateLibraryPeoplePolicy(ctx, owner.ID, spaceID, policy.Version, true, true)
	if err != nil || policy.Version != 1 || !policy.FacesEnabled || !policy.PetsEnabled || policy.QueuedFaceJobs != 1 {
		t.Fatalf("enabled policy = %#v, %v", policy, err)
	}
	invite, _ := database.InviteToSpace(ctx, owner.ID, spaceID, member.Email)
	_, _ = database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true)
	if _, err := database.UpdateLibraryPeoplePolicy(ctx, member.ID, spaceID, policy.Version, false, false); !errors.Is(err, ErrLibraryForbidden) {
		t.Fatalf("member policy update error = %v", err)
	}

	person, err := database.CreateLibraryPerson(ctx, owner.ID, spaceID, "person", "Alex", []string{item.ID})
	if err != nil || person.ItemCount != 1 {
		t.Fatalf("CreateLibraryPerson() = %#v, %v", person, err)
	}
	person, err = database.UpdateLibraryPerson(ctx, owner.ID, spaceID, person.ID, person.Version, "Alex Morgan", item.ID)
	if err != nil || person.Name != "Alex Morgan" || person.CoverItemID != item.ID {
		t.Fatalf("UpdateLibraryPerson() = %#v, %v", person, err)
	}
	pet, err := database.CreateLibraryPerson(ctx, owner.ID, spaceID, "pet", "Pixel", []string{item.ID})
	if err != nil || pet.Kind != "pet" {
		t.Fatalf("create pet = %#v, %v", pet, err)
	}
	people, err := database.LibraryPeople(ctx, owner.ID, spaceID)
	if err != nil || len(people) != 2 {
		t.Fatalf("LibraryPeople() = %#v, %v", people, err)
	}
	items, err := database.LibraryPersonItems(ctx, owner.ID, spaceID, person.ID, 10)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("LibraryPersonItems() = %#v, %v", items, err)
	}

	second, err := database.CreateLibraryPerson(ctx, owner.ID, spaceID, "person", "A. Morgan", []string{item.ID})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := database.MergeLibraryPeople(ctx, owner.ID, spaceID, second.ID, person.ID, second.Version, person.Version)
	if err != nil || merged.ItemCount != 1 {
		t.Fatalf("MergeLibraryPeople() = %#v, %v", merged, err)
	}
	people, _ = database.LibraryPeople(ctx, owner.ID, spaceID)
	if len(people) != 2 {
		t.Fatalf("active people after merge = %#v", people)
	}

	otherSpace, err := database.CreateSpace(ctx, owner.ID, "Other people domain")
	if err != nil {
		t.Fatal(err)
	}
	otherItem := createPeopleTestImage(t, database, owner.ID, otherSpace.ID, "other.jpg", "d")
	if _, err := database.AddLibraryPersonItems(ctx, owner.ID, spaceID, person.ID, []string{otherItem.ID}); !errors.Is(err, ErrLibraryInvalid) {
		t.Fatalf("cross-Space assignment error = %v", err)
	}

	if err := database.DeleteLibraryPerson(ctx, owner.ID, spaceID, pet.ID, pet.Version); err != nil {
		t.Fatalf("DeleteLibraryPerson() error = %v", err)
	}
	policy, err = database.UpdateLibraryPeoplePolicy(ctx, owner.ID, spaceID, policy.Version, false, false)
	if err != nil || policy.FacesEnabled || policy.PetsEnabled || policy.QueuedFaceJobs != 0 {
		t.Fatalf("disabled policy = %#v, %v", policy, err)
	}
}

func createPeopleTestImage(t *testing.T, database *Database, userID, spaceID, filename, digestCharacter string) *SpaceLibraryItem {
	t.Helper()
	digest := strings.Repeat(digestCharacter, 64)
	token := "people-token-" + digestCharacter
	upload, err := database.CreateLibraryUpload(context.Background(), userID, spaceID, "library", filename, "image/jpeg", 128, digest, "library/people-"+digestCharacter, token, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetLibraryUploadState(context.Background(), userID, spaceID, upload.ID, token, "initiated", "uploaded_unverified"); err != nil {
		t.Fatal(err)
	}
	result, err := database.CompleteLibraryUpload(context.Background(), userID, spaceID, upload.ID, token, 128, digest, "image/jpeg", nil)
	if err != nil || result.Item == nil {
		t.Fatalf("complete image = %#v, %v", result, err)
	}
	return result.Item
}

func TestAutomaticPeopleJobsClusterOnlyInsideSpace(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Automatic People Owner", "automatic-people@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Automatic people").ID
	first := createPeopleTestImage(t, database, owner.ID, spaceID, "first-face.jpg", "e")
	second := createPeopleTestImage(t, database, owner.ID, spaceID, "second-face.jpg", "f")
	policy, err := database.UpdateLibraryPeoplePolicy(ctx, owner.ID, spaceID, 0, true, false)
	if err != nil || policy.QueuedFaceJobs != 2 {
		t.Fatalf("enable automatic People = %#v, %v", policy, err)
	}
	vector := make([]float64, 16)
	vector[0], vector[1] = 1, 0.1
	processed := map[string]bool{}
	for range 2 {
		job, err := database.ClaimLibraryPeopleJob(ctx, "people-test-worker", time.Minute)
		if err != nil || job == nil {
			t.Fatalf("ClaimLibraryPeopleJob() = %#v, %v", job, err)
		}
		processed[job.ItemID] = true
		if err := database.CompleteLibraryPeopleJob(ctx, job, []LibraryPeopleDetection{{Kind: "person", Confidence: 0.98, Bounds: []byte(`{"x":0.1,"y":0.1,"width":0.4,"height":0.4}`), Embedding: vector}}); err != nil {
			t.Fatalf("CompleteLibraryPeopleJob() error = %v", err)
		}
	}
	if !processed[first.ID] || !processed[second.ID] {
		t.Fatalf("processed items = %#v", processed)
	}
	people, err := database.LibraryPeople(ctx, owner.ID, spaceID)
	if err != nil || len(people) != 1 || people[0].ItemCount != 2 || people[0].Kind != "person" {
		t.Fatalf("automatic clusters = %#v, %v", people, err)
	}
	items, err := database.LibraryPersonItems(ctx, owner.ID, spaceID, people[0].ID, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("automatic cluster items = %#v, %v", items, err)
	}
	policy, err = database.LibraryPeoplePolicy(ctx, owner.ID, spaceID)
	if err != nil || policy.QueuedFaceJobs != 0 {
		t.Fatalf("completed policy jobs = %#v, %v", policy, err)
	}
}
