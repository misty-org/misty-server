package db

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/lib/pq"
)

type deviceSignInHub struct {
	mu       sync.Mutex
	listener *pq.Listener
	waiters  map[string]map[chan struct{}]struct{}
}

var deviceSignInHubs = struct {
	sync.Mutex
	byDatabase map[*Database]*deviceSignInHub
}{byDatabase: map[*Database]*deviceSignInHub{}}

func (h *deviceSignInHub) wake(idHash string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if idHash == "" {
		for _, waiters := range h.waiters {
			for ch := range waiters {
				nudgeDeviceWaiter(ch)
			}
		}
		return
	}
	for ch := range h.waiters[idHash] {
		nudgeDeviceWaiter(ch)
	}
}

func nudgeDeviceWaiter(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (h *deviceSignInHub) run() {
	for notification := range h.listener.Notify {
		if notification == nil {
			h.wake("")
			continue
		}
		h.wake(notification.Extra)
	}
}

func listenForDeviceSignIns(ctx context.Context, dsn string) (*pq.Listener, error) {
	listener := pq.NewListener(dsn+" connect_timeout=5", time.Second, time.Minute, nil)
	ready := make(chan error, 1)
	go func() {
		if err := listener.Listen(deviceSignInChannel); err != nil {
			ready <- err
			return
		}
		ready <- listener.Ping()
	}()
	var err error
	select {
	case err = <-ready:
	case <-ctx.Done():
		err = ctx.Err()
	case <-time.After(5 * time.Second):
		err = errors.New("device sign-in events unavailable")
	}
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

func (db *Database) WatchDeviceSignIn(ctx context.Context, idHash string) (<-chan struct{}, func(), error) {
	if idHash == "" {
		return nil, nil, errors.New("device sign-in required")
	}
	deviceSignInHubs.Lock()
	defer deviceSignInHubs.Unlock()
	hub := deviceSignInHubs.byDatabase[db]
	if hub == nil {
		listener, err := listenForDeviceSignIns(ctx, db.GetDSN())
		if err != nil {
			return nil, nil, err
		}
		hub = &deviceSignInHub{listener: listener, waiters: map[string]map[chan struct{}]struct{}{}}
		deviceSignInHubs.byDatabase[db] = hub
		go hub.run()
	}
	ch := make(chan struct{}, 1)
	hub.mu.Lock()
	if hub.waiters[idHash] == nil {
		hub.waiters[idHash] = map[chan struct{}]struct{}{}
	}
	hub.waiters[idHash][ch] = struct{}{}
	hub.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			deviceSignInHubs.Lock()
			defer deviceSignInHubs.Unlock()
			hub.mu.Lock()
			delete(hub.waiters[idHash], ch)
			if len(hub.waiters[idHash]) == 0 {
				delete(hub.waiters, idHash)
			}
			empty := len(hub.waiters) == 0
			hub.mu.Unlock()
			if empty && deviceSignInHubs.byDatabase[db] == hub {
				delete(deviceSignInHubs.byDatabase, db)
				_ = hub.listener.Close()
			}
		})
	}, nil
}
