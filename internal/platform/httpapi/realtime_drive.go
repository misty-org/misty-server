package api

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

const maxDriveHolds = 4096

type driveHold struct {
	spaceID  string
	itemID   string
	deviceID string
}

func (s *RealtimeService) holdDrive(client *RealtimeClient, spaceID, itemID, deviceID, deviceName string, active, editing bool) {
	if spaceID == "" || itemID == "" {
		return
	}
	device := db.DriveDevice{ID: strings.ToLower(strings.TrimSpace(deviceID)), Name: deviceName}
	key := spaceID + "\x00" + itemID + "\x00" + device.ID
	s.Mu.Lock()
	select {
	case <-client.Done:
		s.Mu.Unlock()
		return
	default:
	}
	if active {
		if client.driveHolds == nil {
			client.driveHolds = map[string]driveHold{}
		}
		if _, known := client.driveHolds[key]; !known && len(client.driveHolds) >= maxDriveHolds {
			s.Mu.Unlock()
			return
		}
		client.driveHolds[key] = driveHold{spaceID: spaceID, itemID: itemID, deviceID: device.ID}
	} else {
		delete(client.driveHolds, key)
	}
	s.Mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !active {
		if err := s.database.ReleaseDriveInUse(ctx, client.UserID, spaceID, itemID, device.ID); err != nil {
			log.Printf("Realtime drive hold for user %s wasn't released: %v", client.UserID, err)
		}
		return
	}
	if s.holds == nil {
		s.Mu.Lock()
		delete(client.driveHolds, key)
		s.Mu.Unlock()
		return
	}
	if err := s.database.HoldDriveInUse(ctx, client.UserID, spaceID, itemID, device, editing, s.holds.Key); err != nil {
		s.Mu.Lock()
		delete(client.driveHolds, key)
		s.Mu.Unlock()
		return
	}
	select {
	case <-client.Done:
		if err := s.database.ReleaseDriveInUse(ctx, client.UserID, spaceID, itemID, device.ID); err != nil {
			log.Printf("Realtime drive hold for user %s wasn't released: %v", client.UserID, err)
		}
	default:
	}
}

func (s *RealtimeService) releaseDriveHolds(userID string, holds map[string]driveHold) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, hold := range holds {
		if err := s.database.ReleaseDriveInUse(ctx, userID, hold.spaceID, hold.itemID, hold.deviceID); err != nil {
			log.Printf("Realtime drive hold for user %s wasn't released: %v", userID, err)
		}
	}
}
