package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/lib/pq"

	"github.com/misty-org/misty-server/internal/collab"
	"github.com/misty-org/misty-server/internal/collab/admit"
	"github.com/misty-org/misty-server/internal/collab/file"
	"github.com/misty-org/misty-server/internal/collab/history"
	"github.com/misty-org/misty-server/internal/platform/config"
	"github.com/misty-org/misty-server/internal/platform/db"
	api "github.com/misty-org/misty-server/internal/platform/httpapi"
	"github.com/misty-org/misty-server/internal/platform/security"
)

const (
	collabDefaultPort   = "8090"
	collabShutdownGrace = 20 * time.Second
)

var (
	_ collab.Store  = (*db.CollabStore)(nil)
	_ history.Store = (*db.CollabStore)(nil)
	_ file.Store    = (*db.CollabStore)(nil)
)

type CollabService struct {
	Database *db.Database
	Store    *db.CollabStore
	Rooms    *collab.Rooms
	Router   *chi.Mux
	listener *pq.Listener
}

func NewCollabService(database *db.Database) (*CollabService, error) {
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		return nil, err
	}
	library, err := LibraryStoreFromEnv()
	if err != nil {
		return nil, err
	}
	drive, err := api.NewSpaceDriveService(database, library)
	if err != nil {
		return nil, fmt.Errorf("configure Space drive: %w", err)
	}
	store := database.CollabStore()
	public := collabURLFromEnv()
	self := strings.TrimRight(strings.TrimSpace(config.Getenv("MISTY_COLLAB_SELF_URL")), "/")
	rooms := collab.New(collab.Config{
		Store:     store,
		Gate:      collabGate(database, signer, publicAPIOrigin()),
		Bindings:  []collab.BindingFactory{history.Factory(store, store.HistoryDays), file.Factory(drive.CollabDrive(), store)},
		SelfURL:   self,
		PublicURL: public,
	})
	store.OnLost = rooms.HandBack
	service := &CollabService{Database: database, Store: store, Rooms: rooms, Router: chi.NewRouter()}
	service.Router.Use(middleware.Recoverer)
	service.Router.Get("/health", service.health())
	(&api.CollabTransports{Rooms: rooms}).Mount(service.Router)
	return service, nil
}

func collabGate(database *db.Database, signer *security.SessionSigner, apiOrigin string) admit.Gate {
	return admit.Gate{
		VerifyPerson: func(token string) (admit.PersonToken, error) {
			claims, err := signer.Verify(token, "access")
			if err != nil {
				return admit.PersonToken{}, admit.ErrUnauthorized
			}
			return admit.PersonToken{UserID: claims.Subject, SessionID: security.SessionPublicID(claims.SessionID)}, nil
		},
		VerifyIntegration: func(token string) (admit.IntegrationToken, error) {
			claims, err := signer.VerifyIntegration(token)
			if err != nil {
				return admit.IntegrationToken{}, admit.ErrUnauthorized
			}
			return admit.IntegrationToken{ID: claims.Subject, SpaceID: claims.SpaceID, Access: claims.Access,
				KeyPrefix: claims.KeyPrefix, Name: claims.Name}, nil
		},
		Member: func(ctx context.Context, spaceID, userID string) (admit.Member, error) {
			member, err := database.CollabMember(ctx, spaceID, userID)
			if err != nil {
				return admit.Member{}, collabAccessError(err)
			}
			out := admit.Member{Name: member.Name}
			if member.AvatarVersion > 0 {
				out.Avatar = fmt.Sprintf("%s/v1/spaces/%s/members/%s/avatar?v=%d", apiOrigin, spaceID, userID, member.AvatarVersion)
			}
			return out, nil
		},
		SpaceExists: database.CollabSpaceExists,
		File: func(ctx context.Context, spaceID, itemID, userID string) (admit.FileAccess, error) {
			read, write, err := database.CollabFileAccess(ctx, spaceID, itemID, userID)
			if err != nil {
				return admit.FileAccess{}, collabAccessError(err)
			}
			return admit.FileAccess{Read: read, Write: write}, nil
		},
	}
}

func collabAccessError(err error) error {
	switch {
	case errors.Is(err, db.ErrSpaceNotFound):
		return admit.ErrSpaceMissing
	case errors.Is(err, db.ErrSpaceForbidden):
		return admit.ErrNotMember
	case errors.Is(err, db.ErrDriveNotFound):
		return admit.ErrFileMissing
	}
	return err
}

func (service *CollabService) health() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := service.Database.Conn.PingContext(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		if service.listener != nil {
			if err := service.listener.Ping(); err != nil {
				http.Error(w, "control channel unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		_, _ = w.Write([]byte("ok\n"))
	}
}

func (service *CollabService) listen(ctx context.Context) error {
	service.listener = pq.NewListener(service.Database.GetDSN(), time.Second, 30*time.Second, func(event pq.ListenerEventType, err error) {
		if err != nil {
			log.Printf("collab control channel: %v", err)
		}
		if event == pq.ListenerEventReconnected {
			service.Rooms.Control([]byte(`{"type":"` + collab.SignalAccessRecheck + `"}`))
		}
	})
	if err := service.listener.Listen("misty_space_control"); err != nil {
		return err
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case notification, ok := <-service.listener.Notify:
				if !ok {
					return
				}
				if notification != nil {
					service.Rooms.Control([]byte(notification.Extra))
				}
			}
		}
	}()
	return nil
}

func RunCollab() {
	database := &db.Database{}
	if err := database.Start(); err != nil {
		panic(err)
	}
	defer database.Stop()
	service, err := NewCollabService(database)
	if err != nil {
		panic(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	if err := service.listen(ctx); err != nil {
		panic(err)
	}
	defer service.listener.Close()
	port := strings.TrimSpace(config.Getenv("PORT"))
	if port == "" {
		port = collabDefaultPort
	}
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           service.Router,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    httpMaxHeaderBytes,
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	serveErrors := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrors <- err
			return
		}
		serveErrors <- nil
	}()
	log.Printf("Misty collab running on :%s", port)
	select {
	case err := <-serveErrors:
		if err != nil {
			panic(err)
		}
		return
	case received := <-signals:
		log.Printf("received %s, handing rooms back", received)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), collabShutdownGrace)
	defer cancel()
	service.Rooms.Shutdown(shutdown)
	_ = server.Shutdown(shutdown)
	service.Store.Close()
	log.Println("Misty collab stopped")
}
