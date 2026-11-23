package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

var starterIgnoreDevice = db.DriveDevice{ID: "dev_00000000-0000-4000-8000-0000000019e0", Name: "Misty"}

const starterIgnoreRetry = 10 * time.Minute

const StarterDriveIgnore = `# Anything matching a line here stays on each computer and never syncs to the Space.
# .git, secrets, node_modules, .venv, target, dist, build and other dependency and cache folders never sync either.
# A line starting with ! brings one back, such as !build/
*.log
*.tmp
*.temp
*.swp
*.swo
*~
*.pid
*.crdownload
*.part
~$*
.~lock.*#
.Trash-*/
`

func (s *SpaceDriveService) ensureDriveIgnore(ctx context.Context, userID, spaceID string) {
	if !s.StarterIgnore {
		return
	}
	if at, seen := s.ignoreReady.Load(spaceID); seen {
		if failed, ok := at.(time.Time); !ok || time.Since(failed) < starterIgnoreRetry {
			return
		}
	}
	need, err := s.database.DriveRootIgnoreNeed(ctx, userID, spaceID)
	if err != nil {
		return
	}
	if !need.Missing {
		s.ignoreReady.Store(spaceID, struct{}{})
		return
	}
	if err := s.createDriveIgnore(ctx, need.OwnerID, spaceID); err != nil {
		var conflict db.DriveConflictError
		if errors.As(err, &conflict) {
			s.ignoreReady.Store(spaceID, struct{}{})
			return
		}
		log.Printf("drive starter .mistyignore: %v", err)
		s.ignoreReady.Store(spaceID, time.Now())
		return
	}
	s.ignoreReady.Store(spaceID, struct{}{})
}

func (s *SpaceDriveService) createDriveIgnore(ctx context.Context, ownerID, spaceID string) error {
	ctx = context.WithoutCancel(ctx)
	content := []byte(StarterDriveIgnore)
	digest := sha256.Sum256(content)
	sha := hex.EncodeToString(digest[:])
	size := int64(len(content))
	key := db.NewDriveObjectKey()
	if err := s.Store.Put(ctx, key, bytes.NewReader(content), LibraryObjectMetadata{
		ByteSize: size, SHA256: sha, MIMEType: db.DriveMIMEType,
	}); err != nil {
		return err
	}
	kept, err := s.database.EnsureDriveBlob(ctx, spaceID, key, sha, size)
	if err != nil {
		s.cleanUpDriveObjects(ctx, []string{key}, nil)
		return err
	}
	created := ""
	if kept != key {
		s.cleanUpDriveObjects(ctx, []string{key}, nil)
	} else {
		created = key
	}
	result, err := s.database.CreateDriveItem(ctx, db.DriveCreateInput{
		UserID: ownerID, SpaceID: spaceID, Device: starterIgnoreDevice,
		Name: db.DriveIgnoreName, Kind: "file", SHA256: sha, Size: size, MTimeMS: time.Now().UnixMilli(),
	})
	if err != nil {
		if created != "" {
			s.cleanUpDriveObjects(ctx, nil, []string{created})
		}
		return err
	}
	s.cleanUpDriveObjects(ctx, result.DeleteObjectKeys, nil)
	return nil
}
