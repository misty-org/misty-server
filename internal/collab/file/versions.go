package file

import (
	"context"
	"errors"
	"time"
)

const versionsBound = 5 * time.Second

type Claim string

const (
	ClaimBase    Claim = "base"
	ClaimKept    Claim = "kept"
	ClaimOutside Claim = "outside"
)

var ErrRefused = errors.New("file: the drive refused")

type Stale struct{ Version int64 }

func (s *Stale) Error() string { return "file: the file changed since the room loaded it" }

type Versions struct {
	Version        int64
	ContentVersion int64
	ModifiedBy     string
}

type Drive struct {
	Versions func(ctx context.Context, spaceID, itemID, userID string) (Versions, error)
	SaveText func(ctx context.Context, spaceID, itemID, userID, text string, base int64) (int64, error)
	KeepCopy func(ctx context.Context, spaceID, itemID, userID, text string) (string, error)
}

func ClaimHolds(claim Claim, version int64, drive Versions, saved int64) bool {
	if version > drive.Version {
		return false
	}
	if claim == ClaimBase {
		return drive.ContentVersion == 0 || version >= drive.ContentVersion
	}
	current := drive.ContentVersion
	if current == 0 {
		current = drive.Version
	}
	return current > saved
}

func BaseClaimantHolds(drive Versions, userID string, fromWriter bool) bool {
	if drive.ContentVersion == 0 || drive.Version > drive.ContentVersion || drive.ModifiedBy == "" {
		return fromWriter
	}
	return userID != "" && userID == drive.ModifiedBy
}

func readVersions(drive Drive, spaceID, itemID, userID string) (Versions, bool, error) {
	if drive.Versions == nil || spaceID == "" || itemID == "" || userID == "" {
		return Versions{}, false, errors.New("file: no drive")
	}
	ctx, cancel := context.WithTimeout(context.Background(), versionsBound)
	defer cancel()
	versions, err := drive.Versions(ctx, spaceID, itemID, userID)
	if errors.Is(err, ErrRefused) {
		return Versions{}, true, nil
	}
	if err != nil {
		return Versions{}, false, err
	}
	if versions.Version < 1 {
		return Versions{}, false, errors.New("file: the drive named no version")
	}
	if versions.ContentVersion < 0 {
		versions.ContentVersion = 0
	}
	return versions, false, nil
}
