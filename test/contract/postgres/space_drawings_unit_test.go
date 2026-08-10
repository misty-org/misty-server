package db

import (
	"errors"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestNormalizeDrawingTitle(t *testing.T) {
	t.Run("defaults an empty title", func(t *testing.T) {
		title, err := TestingNormalizeDrawingTitle("   ")
		if err != nil {
			t.Fatalf("normalize empty title: %v", err)
		}
		if title != "Untitled drawing" {
			t.Fatalf("title = %q, want %q", title, "Untitled drawing")
		}
	})

	t.Run("counts characters rather than UTF-8 bytes", func(t *testing.T) {
		title, err := TestingNormalizeDrawingTitle(strings.Repeat("✏️", 100))
		if err != nil {
			t.Fatalf("normalize Unicode title: %v", err)
		}
		if title == "" {
			t.Fatal("normalized title is empty")
		}
	})

	t.Run("rejects more than 200 characters", func(t *testing.T) {
		_, err := TestingNormalizeDrawingTitle(strings.Repeat("a", 201))
		if !errors.Is(err, ErrSpaceInvalid) {
			t.Fatalf("error = %v, want ErrSpaceInvalid", err)
		}
	})
}
