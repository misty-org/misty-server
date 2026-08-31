package db

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSpaceAgentPersistsFinishedFiveParagraphJournalEssay(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Essay Note Owner", "essay-note-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Essay Space")
	if err != nil {
		t.Fatal(err)
	}
	prompt := `Create a notes in journal, name it "The First Operating System", and write a 5 paragraph essay on the first operating system created for computers.`
	paragraphs := []string{
		"The earliest computer operating systems emerged when computers changed from single-purpose calculation machines into shared production systems. GM-NAA I/O, developed in 1956 for IBM's 704 computer, is commonly identified as the first operating system because it automated the transition between jobs instead of requiring operators to prepare each run by hand.",
		"Before systems like GM-NAA I/O, programmers interacted with expensive mainframes through a highly manual process. Operators loaded programs, data, and utility routines separately, and the machine could sit idle between jobs while people rearranged tapes or cards. That idle time made computing slower and far more costly.",
		"General Motors Research Laboratories created GM-NAA I/O with help from North American Aviation. Its central innovation was batch processing: jobs could be collected into a sequence, and a resident monitor would load and run them one after another. Input and output routines were shared so that every program did not need to reinvent them.",
		"GM-NAA I/O was modest compared with modern operating systems. It did not offer windows, personal accounts, or the rich device management people now expect. Even so, it established the essential idea that system software should coordinate hardware and programs, reduce human intervention, and keep the computer productively occupied.",
		"That idea shaped the operating systems that followed, from increasingly capable mainframe monitors to time-sharing systems and eventually the software used on personal computers and phones. GM-NAA I/O matters not because it resembled today's systems, but because it introduced the managerial layer between users' programs and the machine that still defines an operating system's role.",
	}
	markdown := strings.Join(paragraphs, "\n\n")
	createdRaw, err := api.TestingExecuteSpaceConversationTool(
		ctx, database, owner.ID, space.ID, "", prompt, "notes.create",
		json.RawMessage(`{"title":"The First Operating System","markdown":`+strconv.Quote(markdown)+`}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		SpaceNote
		ContentReceipt struct {
			Operation          string `json:"operation"`
			Title              string `json:"title"`
			MarkdownCharacters int    `json:"markdown_characters"`
			MarkdownSHA256     string `json:"markdown_sha256"`
			State              string `json:"state"`
		} `json:"content_receipt"`
	}
	if err := json.Unmarshal(createdRaw, &created); err != nil || created.ID == "" {
		t.Fatalf("created essay note = %s, %v", createdRaw, err)
	}
	if created.ContentReceipt.Operation != "create" || created.ContentReceipt.Title != "The First Operating System" ||
		created.ContentReceipt.MarkdownCharacters != len([]rune(markdown)) || len(created.ContentReceipt.MarkdownSHA256) != 64 ||
		created.ContentReceipt.State != "queued_for_collaboration" {
		t.Fatalf("created essay receipt = %#v", created.ContentReceipt)
	}
	commands, err := database.PendingNoteControlCommands(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	var bootstrap struct {
		Title    string `json:"title"`
		Markdown string `json:"markdown"`
	}
	for _, command := range commands {
		if command.NoteID == created.ID && command.Command == "bootstrap" {
			if err := json.Unmarshal(command.Payload, &bootstrap); err != nil {
				t.Fatal(err)
			}
		}
	}
	if bootstrap.Title != "The First Operating System" {
		t.Fatalf("bootstrap title = %q", bootstrap.Title)
	}
	if got := len(strings.Split(bootstrap.Markdown, "\n\n")); got != 5 {
		t.Fatalf("bootstrap paragraph count = %d, want 5: %q", got, bootstrap.Markdown)
	}
	if applied, err := database.ApplySpaceNoteProjection(ctx, SpaceNoteProjection{
		NoteID: created.ID, Revision: 1, Title: bootstrap.Title,
		Markdown: bootstrap.Markdown, PlainText: strings.ReplaceAll(bootstrap.Markdown, "\n\n", "\n"),
	}); err != nil || !applied {
		t.Fatalf("apply essay projection = %v, %v", applied, err)
	}
	stored, err := database.SpaceNoteByID(ctx, owner.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.TitleProjection != "The First Operating System" || stored.MarkdownProjection != markdown {
		t.Fatalf("stored essay note = %#v", stored)
	}
}
