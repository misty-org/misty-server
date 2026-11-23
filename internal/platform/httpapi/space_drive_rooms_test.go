package api

import (
	"testing"

	"github.com/misty-org/misty-server/internal/platform/db"
)

func TestDriveFileFormatKeepsPlainTextOnTheServerAndTheRestWithTheApp(t *testing.T) {
	for name, want := range map[string]string{
		"notes.md": "text", "README.MD": "text", "main.go": "text", "data.csv": "text",
		"poster.excalidraw": "app", "flow.drawio": "app", "photo.png": "app", "Makefile": "app",
	} {
		if got := DriveFileFormat(name); got != want {
			t.Errorf("DriveFileFormat(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestFileRoomDeviceIsAValidDriveDevice(t *testing.T) {
	if !db.ValidDriveDevice(fileRoomDevice) {
		t.Fatal("the live document's device must pass the drive's device check")
	}
}
