package api

import (
	"net/http"
	"testing"
)

type driveContentVersioned struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Version        int64  `json:"version"`
	ContentVersion *int64 `json:"content_version"`
	MTimeMS        int64  `json:"mtime_ms"`
}

func TestDriveItemsCarryTheirContentVersion(t *testing.T) {
	f := newDriveFixture(t, "drive-content-version")
	notes := f.file(f.owner, f.ownerID, nil, "notes.txt", "first")
	path := f.itemPath(notes.ID)
	read := func() driveContentVersioned {
		t.Helper()
		return driveJSON[driveContentVersioned](t, f.call(f.owner, http.MethodGet, path, 0, nil), http.StatusOK)
	}
	listed := func() driveContentVersioned {
		t.Helper()
		page := driveJSON[struct {
			Items []driveContentVersioned `json:"items"`
		}](t, f.call(f.owner, http.MethodGet, f.base+"/changes?since=0", 0, nil), http.StatusOK)
		for _, item := range page.Items {
			if item.ID == notes.ID {
				return item
			}
		}
		t.Fatalf("changes feed has no %s", notes.ID)
		return driveContentVersioned{}
	}

	created := read()
	if created.ContentVersion == nil || *created.ContentVersion != 1 || created.Version != 1 {
		t.Fatalf("created item = %+v", created)
	}

	renamed := driveJSON[driveContentVersioned](t, f.call(f.owner, http.MethodPatch, path, 0, map[string]any{"name": "Notes.txt"}), http.StatusOK)
	if renamed.ContentVersion == nil || *renamed.ContentVersion != 1 || renamed.Version != 2 {
		t.Fatalf("renamed item = %+v", renamed)
	}
	if feed := listed(); feed.ContentVersion == nil || *feed.ContentVersion != 1 || feed.Version != 2 {
		t.Fatalf("renamed item in the changes feed = %+v", feed)
	}

	uploadID, sha := f.upload(f.member, f.memberID, []byte("second"))
	changed := driveJSON[driveContentVersioned](t, f.call(f.member, http.MethodPatch, path, 0,
		map[string]any{"sha256": sha, "size": 6, "upload_id": uploadID, "base_version": renamed.Version}), http.StatusOK)
	if changed.ContentVersion == nil || *changed.ContentVersion != changed.Version || changed.Version != 3 {
		t.Fatalf("changed item = %+v", changed)
	}
	if changed.MTimeMS == renamed.MTimeMS {
		t.Fatalf("content change without mtime_ms kept the old mtime %d", changed.MTimeMS)
	}
	if feed := listed(); feed.ContentVersion == nil || *feed.ContentVersion != changed.Version {
		t.Fatalf("changed item in the changes feed = %+v", feed)
	}
	if got := read(); got.ContentVersion == nil || *got.ContentVersion != changed.Version {
		t.Fatalf("changed item by id = %+v", got)
	}
}

func TestDriveRefusesASaveFromAVersionItNeverHad(t *testing.T) {
	f := newDriveFixture(t, "drive-base-ahead")
	notes := f.file(f.owner, f.ownerID, nil, "notes.txt", "first")
	path := f.itemPath(notes.ID)

	uploadID, sha := f.upload(f.owner, f.ownerID, []byte("from the future"))
	ahead := driveJSON[map[string]any](t, f.call(f.owner, http.MethodPatch, path, 0,
		map[string]any{"sha256": sha, "size": 15, "upload_id": uploadID, "mtime_ms": 1800000000000, "base_version": notes.Version + 1}),
		http.StatusConflict)
	if ahead["code"] != "version_conflict" {
		t.Fatalf("base above the version = %v", ahead)
	}
	item, _ := ahead["item"].(map[string]any)
	if item == nil || item["version"].(float64) != float64(notes.Version) || item["content_version"].(float64) != 1 {
		t.Fatalf("conflict item = %v", ahead["item"])
	}
	if got := driveJSON[driveContentVersioned](t, f.call(f.owner, http.MethodGet, path, 0, nil), http.StatusOK); got.Version != notes.Version {
		t.Fatalf("refused save changed the item: %+v", got)
	}

	uploadID, sha = f.upload(f.owner, f.ownerID, []byte("from now"))
	current := driveJSON[driveContentVersioned](t, f.call(f.owner, http.MethodPatch, path, 0,
		map[string]any{"sha256": sha, "size": 8, "upload_id": uploadID, "mtime_ms": 1800000000000, "base_version": notes.Version}),
		http.StatusOK)
	if current.Version != notes.Version+1 || current.ContentVersion == nil || *current.ContentVersion != current.Version {
		t.Fatalf("save from the current version = %+v", current)
	}

	renamed := driveJSON[driveContentVersioned](t, f.call(f.owner, http.MethodPatch, path, 0,
		map[string]any{"name": "Notes.txt", "base_version": current.Version + 5}), http.StatusOK)
	if renamed.Name != "Notes.txt" {
		t.Fatalf("rename with a base ahead = %+v", renamed)
	}
}
