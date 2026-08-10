// Command journal-collab-ticket mints a Journal collaboration ticket from the
// command line using the server's real configuration.
//
// It exists so the Go signer and the Cloudflare verifier can be checked against
// each other for real. Unit tests on both sides passing proves only that each
// agrees with itself; this proves they agree on the wire format.
//
//	JOURNAL_COLLAB_TICKET_PRIVATE_KEY=... JOURNAL_COLLAB_CONTROL_SECRET=... \
//	JOURNAL_COLLAB_PROJECTION_SECRET=... JOURNAL_COLLAB_ROOM_SALT=... \
//	go run ./cmd/journal-collab-ticket -drawing drawing_demo -user user_go -role editor
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func main() {
	noteID := flag.String("note", "", "note id")
	drawingID := flag.String("drawing", "", "drawing id")
	userID := flag.String("user", "", "user id")
	spaceID := flag.String("space", "space_demo", "space id")
	role := flag.String("role", "editor", "creator, editor, or viewer")
	aclVersion := flag.Int64("acl-version", 1, "resource ACL version")
	flag.Parse()

	if (*noteID == "") == (*drawingID == "") || *userID == "" {
		fmt.Fprintln(os.Stderr, "exactly one of -note or -drawing, plus -user, is required")
		os.Exit(2)
	}

	config, err := api.JournalCollabConfigFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration: %v\n", err)
		os.Exit(1)
	}
	var ticket api.JournalTicket
	if *noteID != "" {
		ticket, err = config.MintNoteTicket(
			*userID,
			*spaceID,
			*noteID,
			*role,
			*aclVersion,
		)
	} else {
		ticket, err = config.MintDrawingTicket(
			*userID,
			*spaceID,
			*drawingID,
			*role,
			*aclVersion,
		)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "minting: %v\n", err)
		os.Exit(1)
	}
	encoded, err := json.Marshal(ticket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoding: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}
