package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
	"github.com/misty-org/misty-server/internal/spaces"
)

var device = db.DriveDevice{ID: "dev_00000000-0000-4000-8000-0000000c011a", Name: "Collab runtime tests"}

type fixture struct {
	database *db.Database
	signer   *security.SessionSigner
}

type handler func(ctx context.Context, body json.RawMessage) (any, error)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "where to listen")
	flag.Parse()
	database := &db.Database{}
	if err := database.Start(); err != nil {
		log.Fatal(err)
	}
	defer database.Stop()
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	f := &fixture{database: database, signer: signer}
	mux := http.NewServeMux()
	for path, h := range map[string]handler{
		"/users":               f.user,
		"/spaces":              f.space,
		"/spaces/delete":       f.deleteSpace,
		"/members/remove":      f.removeMember,
		"/roles/everyone":      f.everyone,
		"/files":               f.file,
		"/files/write":         f.write,
		"/tokens/person":       f.personToken,
		"/tokens/integration":  f.integrationToken,
		"/integrations":        f.integration,
		"/integrations/rename": f.renameIntegration,
		"/signals/session":     f.sessionEnded,
		"/signals/integration": f.integrationEnded,
	} {
		mux.Handle(path, serve(h))
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		<-signals
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	fmt.Printf("fixture listening on %s\n", listener.Addr().String())
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func serve(h handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out, err := h(r.Context(), body)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	})
}

func read[T any](body json.RawMessage) (T, error) {
	var value T
	err := json.Unmarshal(body, &value)
	return value, err
}

func randomHex(n int) string {
	raw := make([]byte, n)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

func (f *fixture) user(_ context.Context, body json.RawMessage) (any, error) {
	in, err := read[struct {
		Username string `json:"username"`
	}](body)
	if err != nil {
		return nil, err
	}
	user, err := f.database.CreateUserWithUsername(in.Username, in.Username+"@collab.test", "collab-runtime-"+randomHex(8))
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": user.ID, "username": in.Username}, nil
}

func (f *fixture) space(ctx context.Context, body json.RawMessage) (any, error) {
	in, err := read[struct {
		Owner   string   `json:"owner"`
		Members []string `json:"members"`
		Name    string   `json:"name"`
	}](body)
	if err != nil {
		return nil, err
	}
	space, err := f.database.TestingCreateSpace(ctx, in.Owner, in.Name)
	if err != nil {
		return nil, err
	}
	for _, member := range in.Members {
		user, err := f.database.GetUserByID(member)
		if err != nil {
			return nil, err
		}
		invite, err := f.database.TestingInviteToSpace(ctx, in.Owner, space.ID, user.Email)
		if err != nil {
			return nil, err
		}
		if _, err := f.database.RespondToSpaceInvite(ctx, member, invite.ID, true); err != nil {
			return nil, err
		}
	}
	return map[string]string{"id": space.ID, "name": space.Name}, nil
}

func (f *fixture) deleteSpace(ctx context.Context, body json.RawMessage) (any, error) {
	in, err := read[struct {
		Owner string `json:"owner"`
		Space string `json:"space"`
		Name  string `json:"name"`
	}](body)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, f.database.DeleteSpace(ctx, in.Owner, in.Space, in.Name)
}

func (f *fixture) removeMember(ctx context.Context, body json.RawMessage) (any, error) {
	in, err := read[struct {
		Owner  string `json:"owner"`
		Space  string `json:"space"`
		Member string `json:"member"`
	}](body)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, f.database.RemoveSpaceMember(ctx, in.Owner, in.Space, in.Member)
}

func (f *fixture) everyone(ctx context.Context, body json.RawMessage) (any, error) {
	in, err := read[struct {
		Owner      string `json:"owner"`
		Space      string `json:"space"`
		DriveWrite bool   `json:"drive_write"`
	}](body)
	if err != nil {
		return nil, err
	}
	roles, err := f.database.SpaceRoles(ctx, in.Owner, in.Space)
	if err != nil {
		return nil, err
	}
	for _, role := range roles {
		if !role.IsDefault {
			continue
		}
		permissions := []string{}
		for _, permission := range role.Permissions {
			if permission != spaces.PermissionDriveWrite {
				permissions = append(permissions, permission)
			}
		}
		if in.DriveWrite {
			permissions = append(permissions, spaces.PermissionDriveWrite)
		}
		_, err := f.database.UpdateSpaceRole(ctx, in.Owner, in.Space, role.ID, db.SpaceRoleInput{Permissions: &permissions})
		return map[string]bool{"ok": true}, err
	}
	return nil, errors.New("the Space has no default role")
}

func contentOf(text string) (string, int64) {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:]), int64(len(text))
}

func (f *fixture) blob(ctx context.Context, spaceID, text string) (string, int64, error) {
	sha, size := contentOf(text)
	if size == 0 {
		return sha, size, nil
	}
	_, err := f.database.EnsureDriveBlob(ctx, spaceID, db.NewDriveObjectKey(), sha, size)
	return sha, size, err
}

func (f *fixture) file(ctx context.Context, body json.RawMessage) (any, error) {
	in, err := read[struct {
		User  string `json:"user"`
		Space string `json:"space"`
		Name  string `json:"name"`
		Text  string `json:"text"`
	}](body)
	if err != nil {
		return nil, err
	}
	sha, size, err := f.blob(ctx, in.Space, in.Text)
	if err != nil {
		return nil, err
	}
	result, err := f.database.CreateDriveItem(ctx, db.DriveCreateInput{UserID: in.User, SpaceID: in.Space, Device: device,
		Name: in.Name, Kind: "file", SHA256: sha, Size: size, MTimeMS: time.Now().UnixMilli()})
	if err != nil {
		return nil, err
	}
	return result.Item, nil
}

func (f *fixture) write(ctx context.Context, body json.RawMessage) (any, error) {
	in, err := read[struct {
		User  string  `json:"user"`
		Space string  `json:"space"`
		Item  string  `json:"item"`
		Text  *string `json:"text"`
		Name  *string `json:"name"`
	}](body)
	if err != nil {
		return nil, err
	}
	item, err := f.database.DriveItem(ctx, in.User, in.Space, in.Item)
	if err != nil {
		return nil, err
	}
	mtime := time.Now().UnixMilli()
	patch := db.DrivePatchInput{UserID: in.User, SpaceID: in.Space, ItemID: in.Item, Device: device,
		IfMatch: item.Version, BaseVersion: item.Version, Name: in.Name, MTimeMS: &mtime}
	if in.Text != nil {
		sha, size, err := f.blob(ctx, in.Space, *in.Text)
		if err != nil {
			return nil, err
		}
		patch.Content, patch.SHA256, patch.Size = true, sha, size
	}
	result, err := f.database.PatchDriveItem(ctx, patch)
	if err != nil {
		return nil, err
	}
	return result.Item, nil
}

func (f *fixture) personToken(_ context.Context, body json.RawMessage) (any, error) {
	in, err := read[struct {
		User    string `json:"user"`
		Session string `json:"session"`
		TTL     int    `json:"ttl_seconds"`
	}](body)
	if err != nil {
		return nil, err
	}
	if in.Session == "" {
		in.Session = randomHex(16)
	}
	ttl := time.Duration(in.TTL) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	token, err := f.signer.Mint(in.User, in.Session, "access", time.Now().Add(ttl))
	if err != nil {
		return nil, err
	}
	return map[string]string{"token": token, "session": in.Session, "public_session": security.SessionPublicID(in.Session)}, nil
}

func (f *fixture) integration(ctx context.Context, body json.RawMessage) (any, error) {
	in, err := read[struct {
		Owner     string   `json:"owner"`
		Space     string   `json:"space"`
		Name      string   `json:"name"`
		Access    []string `json:"access"`
		KeyPrefix string   `json:"key_prefix"`
	}](body)
	if err != nil {
		return nil, err
	}
	_, hash, err := spaces.NewIntegrationToken()
	if err != nil {
		return nil, err
	}
	created, err := f.database.CreateSpaceIntegration(ctx, in.Owner, in.Space, db.SpaceIntegrationInput{Name: in.Name,
		Avatar: json.RawMessage(`{"emoji":"🤖"}`), Access: in.Access, KeyPrefix: in.KeyPrefix}, hash)
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (f *fixture) integrationToken(ctx context.Context, body json.RawMessage) (any, error) {
	in, err := read[struct {
		Owner string `json:"owner"`
		Space string `json:"space"`
		ID    string `json:"id"`
	}](body)
	if err != nil {
		return nil, err
	}
	listed, err := f.database.SpaceIntegrations(ctx, in.Owner, in.Space)
	if err != nil {
		return nil, err
	}
	for _, integration := range listed {
		if integration.ID != in.ID {
			continue
		}
		token, err := f.signer.MintIntegration(security.IntegrationClaims{Subject: integration.ID, SpaceID: in.Space,
			Access: integration.Access, KeyPrefix: integration.KeyPrefix, Name: integration.Name}, time.Now().Add(time.Hour))
		if err != nil {
			return nil, err
		}
		return map[string]string{"token": token, "name": integration.Name}, nil
	}
	return nil, errors.New("no such integration")
}

func (f *fixture) renameIntegration(ctx context.Context, body json.RawMessage) (any, error) {
	in, err := read[struct {
		Owner string `json:"owner"`
		Space string `json:"space"`
		ID    string `json:"id"`
		Name  string `json:"name"`
	}](body)
	if err != nil {
		return nil, err
	}
	return f.database.UpdateSpaceIntegration(ctx, in.Owner, in.Space, in.ID, db.SpaceIntegrationPatch{Name: &in.Name})
}

func (f *fixture) sessionEnded(ctx context.Context, body json.RawMessage) (any, error) {
	in, err := read[struct {
		Session string `json:"session"`
	}](body)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, f.database.SignalCollabSessionEnded(ctx, security.SessionPublicID(in.Session))
}

func (f *fixture) integrationEnded(ctx context.Context, body json.RawMessage) (any, error) {
	in, err := read[struct {
		Owner string `json:"owner"`
		Space string `json:"space"`
		ID    string `json:"id"`
	}](body)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, f.database.RemoveSpaceIntegration(ctx, in.Owner, in.Space, in.ID)
}
