package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// Handoff is written to Config.HandoffPath so `misty server up --gui` can find and
// reopen a running console. It holds the launch token, so it is 0600 and is
// removed on shutdown.
type Handoff struct {
	PID       int       `json:"pid"`
	URL       string    `json:"url"`
	StartedAt time.Time `json:"started_at"`
}

// Run serves the console until ctx is cancelled. The launch URL is printed on
// stdout as the only line written there.
func Run(ctx context.Context, cfg Config, database *db.Database) error {
	if err := ValidateLoopback(cfg.Addr); err != nil {
		return err
	}
	token, err := security.GenerateSecureToken()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Addr, err)
	}
	launchURL := "http://" + listener.Addr().String() + "/?token=" + token

	if cfg.HandoffPath != "" {
		if err := writeHandoff(cfg.HandoffPath, Handoff{PID: os.Getpid(), URL: launchURL, StartedAt: time.Now().UTC()}); err != nil {
			_ = listener.Close()
			return err
		}
		defer os.Remove(cfg.HandoffPath)
	}

	server := &http.Server{
		Handler:           NewServer(cfg, database, token).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errs := make(chan error, 1)
	go func() { errs <- server.Serve(listener) }()
	fmt.Println(launchURL)

	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func writeHandoff(path string, handoff Handoff) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(handoff)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
