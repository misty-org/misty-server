package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/misty-org/misty-server/internal/app"
	"github.com/misty-org/misty-server/internal/platform/config"
	"github.com/misty-org/misty-server/internal/platform/db"
)

func main() {
	if err := config.ApplyServerDefaults(); err != nil {
		log.Fatal(err)
	}
	database := &db.Database{}
	if err := database.Start(); err != nil {
		log.Fatal(err)
	}
	defer database.Stop()
	service, err := app.NewCollabService(database)
	if err != nil {
		log.Fatal(err)
	}
	defer service.Close()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	if err := service.Start(ctx); err != nil {
		log.Fatal(err)
	}
	port := strings.TrimSpace(config.Getenv("PORT"))
	if port == "" {
		port = "8090"
	}
	server := &http.Server{Addr: ":" + port, Handler: service.Router, ReadHeaderTimeout: 10 * time.Second}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
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
			log.Fatal(err)
		}
		return
	case received := <-signals:
		log.Printf("received %s, handing rooms back", received)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	service.Stop(shutdown)
	_ = server.Shutdown(shutdown)
	log.Println("Misty collab stopped")
}
