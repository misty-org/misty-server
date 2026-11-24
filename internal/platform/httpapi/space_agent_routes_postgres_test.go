package api

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
	"github.com/misty-org/misty-server/test/testkit"
)

type spaceRouteFixture struct {
	t        *testing.T
	database *db.Database
	router   *chi.Mux
	space    *db.Space
	owner    string
	member   string
	other    string
	stranger string
}

func newSpaceRouteFixture(t *testing.T) *spaceRouteFixture {
	t.Helper()
	if os.Getenv("TEST_DB_NAME") == "" {
		t.Skip("requires a test database")
	}
	database := testkit.OpenDatabase(t)
	ctx := t.Context()
	user := func(name string) string {
		created, err := database.CreateUser(name+"@example.test", "password123")
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	f := &spaceRouteFixture{t: t, database: database, owner: user("owner"), member: user("member"), other: user("other"), stranger: user("stranger")}
	space, err := database.TestingCreateSpace(ctx, f.owner, "Work")
	if err != nil {
		t.Fatal(err)
	}
	f.space = space
	if err := database.SpaceTx(ctx, func(tx *sql.Tx) error {
		for _, id := range []string{f.member, f.other} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')`, space.ID, id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := &SpacesService{database: database}
	f.router = chi.NewRouter()
	f.router.Get("/search", service.Search())
	f.router.Get("/spaces/{spaceID}/notes", service.SpaceNotes())
	f.router.Post("/spaces/{spaceID}/notes", service.SpaceNotes())
	f.router.Put("/spaces/{spaceID}/notes/{noteID}/content", service.SpaceNoteContent())
	f.router.Get("/spaces/{spaceID}/messages/search", service.SearchMessages())
	return f
}

func (f *spaceRouteFixture) do(userID, method, path string, body any, headers ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	var payload io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		payload = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, path, payload)
	if userID != "" {
		signer, err := security.SessionSignerFromEnv()
		if err != nil {
			f.t.Fatal(err)
		}
		token, err := signer.Mint(userID, "person@example.test", "access", time.Now().Add(time.Minute))
		if err != nil {
			f.t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for index := 0; index+1 < len(headers); index += 2 {
		request.Header.Set(headers[index], headers[index+1])
	}
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	return recorder
}

func (f *spaceRouteFixture) ids(recorder *httptest.ResponseRecorder, field string) []string {
	f.t.Helper()
	if recorder.Code != http.StatusOK {
		f.t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		f.t.Fatal(err)
	}
	var items []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body[field], &items); err != nil {
		f.t.Fatal(err)
	}
	ids := []string{}
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func sameIDs(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, id := range want {
		found := false
		for _, candidate := range got {
			found = found || candidate == id
		}
		if !found {
			return false
		}
	}
	return true
}

func TestSpaceSearchRoutesShowOnlyWhatThePersonCanSeePostgres(t *testing.T) {
	f := newSpaceRouteFixture(t)
	ctx := t.Context()
	note := func(creator, title, body string) string {
		created, err := f.database.CreateSpaceNoteWithAudience(ctx, creator, f.space.ID, title,
			db.SpaceResourceAudience{Kind: db.SpaceAudienceSpace}, body)
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	public := note(f.owner, "Launch plan", "The budget review is on Friday")
	titled := note(f.member, "Budget ideas", "Nothing else here")
	secret := note(f.owner, "Private thoughts", "budget worries")
	note(f.owner, "Unrelated", "Lunch menu")
	if err := f.database.SetSpaceItemVisibility(ctx, f.owner, f.space.ID, "note", secret, db.VisibilityPrivate); err != nil {
		t.Fatal(err)
	}
	send := func(sender, conversationID, text string) string {
		content := []db.MessageSpan{{Type: "text", Text: text}}
		if conversationID == "" {
			message, _, err := f.database.TestingCreateSpaceMessage(ctx, sender, f.space.ID, content, nil)
			if err != nil {
				t.Fatal(err)
			}
			return message.ID
		}
		message, _, err := f.database.TestingCreateSpaceConversationMessageWithReferences(ctx, sender, f.space.ID, conversationID, content, nil, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return message.ID
	}
	chat := send(f.owner, "", "Budget is due on Friday")
	dm, err := f.database.DirectMemberConversation(ctx, f.owner, f.space.ID, f.member)
	if err != nil {
		t.Fatal(err)
	}
	private := send(f.owner, dm.ID, "keep the budget between us")
	send(f.owner, "", "nothing to see")

	notes := "/spaces/" + f.space.ID + "/notes?q="
	if got := f.ids(f.do(f.owner, "GET", notes+"budget", nil), "notes"); !sameIDs(got, public, titled, secret) {
		t.Fatalf("owner notes = %v", got)
	}
	if got := f.ids(f.do(f.other, "GET", notes+"BUDG", nil), "notes"); !sameIDs(got, public, titled) {
		t.Fatalf("a member must not find someone else's private note, by word start: %v", got)
	}
	if got := f.ids(f.do(f.other, "GET", notes+"budget+friday", nil), "notes"); !sameIDs(got, public) {
		t.Fatalf("every word has to match: %v", got)
	}
	if got := f.do(f.stranger, "GET", notes+"budget", nil); got.Code == http.StatusOK {
		t.Fatalf("a stranger searched a Space's notes: %s", got.Body.String())
	}

	messages := "/spaces/" + f.space.ID + "/messages/search?q="
	if got := f.ids(f.do(f.member, "GET", messages+"budget", nil), "messages"); !sameIDs(got, chat, private) {
		t.Fatalf("member messages = %v", got)
	}
	if got := f.ids(f.do(f.other, "GET", messages+"budget", nil), "messages"); !sameIDs(got, chat) {
		t.Fatalf("a DM must stay between its two people: %v", got)
	}
	if got := f.ids(f.do(f.other, "GET", messages+"text", nil), "messages"); len(got) != 0 {
		t.Fatalf("a span's type is not a word in the message: %v", got)
	}
	if got := f.do(f.stranger, "GET", messages+"budget", nil); got.Code == http.StatusOK {
		t.Fatalf("a stranger searched a Space's messages: %s", got.Body.String())
	}
	if got := f.do(f.other, "GET", messages, nil); got.Code != http.StatusBadRequest {
		t.Fatalf("an empty search = %d", got.Code)
	}

	if got := f.ids(f.do(f.other, "GET", "/search?q=budget", nil), "results"); !sameIDs(got, public, titled, chat) {
		t.Fatalf("search across Spaces = %v", got)
	}
	if got := f.ids(f.do(f.member, "GET", "/search?q=budget&kinds=message&space_id="+f.space.ID, nil), "results"); !sameIDs(got, chat, private) {
		t.Fatalf("search for messages = %v", got)
	}
	if got := f.ids(f.do(f.stranger, "GET", "/search?q=budget", nil), "results"); len(got) != 0 {
		t.Fatalf("a stranger found %v", got)
	}
	if got := f.do(f.stranger, "GET", "/search?q=budget&space_id="+f.space.ID, nil); got.Code == http.StatusOK {
		t.Fatalf("a stranger searched a named Space: %s", got.Body.String())
	}
	if got := f.do(f.other, "GET", "/search?q=budget&kinds=roadmap", nil); got.Code != http.StatusBadRequest {
		t.Fatalf("an unknown kind = %d", got.Code)
	}
	if got := f.do("", "GET", "/search?q=budget", nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("signed out = %d", got.Code)
	}

	var hit struct {
		Results []map[string]string `json:"results"`
	}
	raw := f.do(f.other, "GET", "/search?q=budget&kinds=message", nil)
	if err := json.Unmarshal(raw.Body.Bytes(), &hit); err != nil || len(hit.Results) != 1 {
		t.Fatalf("hit = %s, %v", raw.Body.String(), err)
	}
	want := map[string]string{"kind": "message", "id": chat, "title": "Space chat", "excerpt": "Budget is due on Friday", "space_id": f.space.ID, "space": "Work"}
	for field, value := range want {
		if hit.Results[0][field] != value {
			t.Errorf("hit %s = %q, want %q", field, hit.Results[0][field], value)
		}
	}
}

func TestSpaceNoteContentRouteSavesTheNotePostgres(t *testing.T) {
	f := newSpaceRouteFixture(t)
	ctx := t.Context()
	path := "/spaces/" + f.space.ID + "/notes"
	long := strings.Repeat("A long line of the note. ", 1000)
	created := f.do(f.member, "POST", path, map[string]string{"title": "Launch", "markdown": long})
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var note struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Receipt struct {
			Operation  string `json:"operation"`
			SHA256     string `json:"markdown_sha256"`
			Characters int    `json:"markdown_characters"`
			State      string `json:"state"`
		} `json:"content_receipt"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &note); err != nil {
		t.Fatal(err)
	}
	digest := func(text string) string {
		sum := sha256.Sum256([]byte(text))
		return hex.EncodeToString(sum[:])
	}
	if note.Title != "Launch" || note.Receipt.Operation != "create" || note.Receipt.SHA256 != digest(long) || note.Receipt.State != "saved" {
		t.Fatalf("create answered %s", created.Body.String())
	}
	if plain := f.do(f.member, "POST", path, map[string]string{"title": "Empty"}); plain.Code != http.StatusCreated || strings.Contains(plain.Body.String(), "content_receipt") {
		t.Fatalf("a note made by hand changed: %d %s", plain.Code, plain.Body.String())
	}

	content := path + "/" + note.ID + "/content"
	if replaced := f.do(f.member, "PUT", content, map[string]string{"markdown": "# Ready"}); replaced.Code != http.StatusOK || !strings.Contains(replaced.Body.String(), digest("# Ready")) {
		t.Fatalf("replace = %d %s", replaced.Code, replaced.Body.String())
	}
	saved, err := f.database.SpaceNoteByID(ctx, f.member, note.ID)
	if err != nil || saved.MarkdownProjection != "# Ready" || saved.PlainTextProjection != "Ready" {
		t.Fatalf("saved note = %+v, %v", saved, err)
	}
	if added := f.do(f.member, "PUT", content, map[string]string{"append": "Another line"}); added.Code != http.StatusOK || !strings.Contains(added.Body.String(), digest("# Ready\n\nAnother line")) {
		t.Fatalf("append = %d %s", added.Code, added.Body.String())
	}
	if renamed := f.do(f.member, "PUT", content, map[string]string{"title": "New title"}); renamed.Code != http.StatusOK || !strings.Contains(renamed.Body.String(), digest("# Ready\n\nAnother line")) {
		t.Fatalf("a new title must keep the body: %d %s", renamed.Code, renamed.Body.String())
	}
	if both := f.do(f.member, "PUT", content, map[string]string{"markdown": "x", "append": "y"}); both.Code != http.StatusBadRequest {
		t.Fatalf("markdown and append = %d", both.Code)
	}
	if stranger := f.do(f.stranger, "PUT", content, map[string]string{"markdown": "x"}); stranger.Code != http.StatusNotFound {
		t.Fatalf("stranger = %d", stranger.Code)
	}
	elsewhere, err := f.database.TestingCreateSpace(ctx, f.member, "Elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	if wrong := f.do(f.member, "PUT", "/spaces/"+elsewhere.ID+"/notes/"+note.ID+"/content", map[string]string{"markdown": "x"}); wrong.Code != http.StatusNotFound {
		t.Fatalf("wrong Space = %d", wrong.Code)
	}
}
