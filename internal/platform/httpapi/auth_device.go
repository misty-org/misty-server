package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/accounts"
	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
)

const deviceSignInKeepalive = 25 * time.Second

func StartDeviceSignIn(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		name, ok := accounts.NormalizeDeviceName(body.Name)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
			return
		}
		id, err := accounts.NewDeviceSignInID()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
			return
		}
		code, _, err := database.CreateDeviceSignIn(r.Context(), accounts.HashDeviceSignInID(id), name)
		if err != nil {
			log.Printf("device sign-in: %v", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "unavailable"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":               id,
			"user_code":        accounts.FormatUserCode(code),
			"verification_url": accounts.DeviceVerificationLink(code),
			"expires_in":       int(accounts.DeviceSignInTTL.Seconds()),
		})
	}
}

func LookupDeviceSignIn(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authenticatedUser(w, r, database); !ok {
			return
		}
		code, ok := accounts.NormalizeUserCode(r.URL.Query().Get("code"))
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
			return
		}
		request, err := database.LookupDeviceSignIn(r.Context(), code)
		if err != nil {
			writeDeviceCodeError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{
			"name":       request.Name,
			"created_at": request.CreatedAt,
			"expires_at": request.ExpiresAt,
		})
	}
}

func ApproveDeviceSignIn(database *db.Database) http.HandlerFunc {
	return decideDeviceSignIn(database, true)
}

func RefuseDeviceSignIn(database *db.Database) http.HandlerFunc {
	return decideDeviceSignIn(database, false)
}

func decideDeviceSignIn(database *db.Database, approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		var body struct {
			UserCode string `json:"user_code"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		code, ok := accounts.NormalizeUserCode(body.UserCode)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
			return
		}
		if err := database.DecideDeviceSignIn(r.Context(), code, userID, approve); err != nil {
			writeDeviceCodeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeDeviceCodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrDeviceCodeNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	writeSpaceError(w, err)
}

func WaitDeviceSignIn(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "deviceID")
		if !accounts.ValidDeviceSignInID(id) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unavailable", http.StatusInternalServerError)
			return
		}
		idHash := accounts.HashDeviceSignInID(id)
		changes, stop, err := database.WatchDeviceSignIn(r.Context(), idHash)
		if err != nil {
			w.Header().Set("Retry-After", "30")
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "unavailable"})
			return
		}
		defer stop()
		request, found, err := database.DeviceSignInByID(r.Context(), idHash)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		_ = http.NewResponseController(w).SetWriteDeadline(request.ExpiresAt.Add(time.Minute))
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store, no-transform")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		keepalive := time.NewTicker(deviceSignInKeepalive)
		defer keepalive.Stop()
		expiry := time.NewTimer(time.Until(request.ExpiresAt))
		defer expiry.Stop()
		for {
			event, payload, done, err := settleDeviceSignIn(r, database, idHash, request, found)
			if err != nil {
				log.Printf("device sign-in wait: %v", err)
				return
			}
			if done {
				writeDeviceSignInEvent(w, event, payload)
				flusher.Flush()
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-keepalive.C:
				if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
					return
				}
				flusher.Flush()
				continue
			case <-changes:
			case <-expiry.C:
			}
			request, found, err = database.DeviceSignInByID(r.Context(), idHash)
			if err != nil {
				log.Printf("device sign-in wait: %v", err)
				return
			}
		}
	}
}

func settleDeviceSignIn(r *http.Request, database *db.Database, idHash string, request db.DeviceSignIn, found bool) (string, any, bool, error) {
	if !found {
		return db.DeviceSignInExpired, struct{}{}, true, nil
	}
	switch request.State {
	case db.DeviceSignInRefused:
		return db.DeviceSignInRefused, struct{}{}, true, nil
	case db.DeviceSignInExpired:
		return db.DeviceSignInExpired, struct{}{}, true, nil
	case db.DeviceSignInPending:
		if !time.Now().Before(request.ExpiresAt) {
			return db.DeviceSignInExpired, struct{}{}, true, nil
		}
		return "", nil, false, nil
	case db.DeviceSignInApproved:
		return deliverDeviceSession(r, database, idHash, request.ApprovedUserID)
	}
	return db.DeviceSignInExpired, struct{}{}, true, nil
}

func deliverDeviceSession(r *http.Request, database *db.Database, idHash, userID string) (string, any, bool, error) {
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		return "", nil, false, err
	}
	sid, err := security.GenerateSecureToken()
	if err != nil {
		return "", nil, false, err
	}
	expires := time.Now().Add(db.SessionTTL)
	access, refresh, err := mintSessionPair(signer, userID, sid, expires)
	if err != nil {
		return "", nil, false, err
	}
	outcome, err := database.CompleteDeviceSignIn(r.Context(), idHash, userID, security.HashToken(sid), security.HashToken(refresh), expires)
	if err != nil {
		return "", nil, false, err
	}
	switch outcome {
	case db.DeviceSignInDelivered:
		return db.DeviceSignInApproved, map[string]any{
			"session_id":    security.SessionPublicID(sid),
			"refresh_token": refresh,
			"access_token":  access,
			"expires_in":    int(security.AccessTokenTTL.Seconds()),
		}, true, nil
	case db.DeviceSignInRefusedAtDelivery:
		return db.DeviceSignInRefused, struct{}{}, true, nil
	}
	request, found, err := database.DeviceSignInByID(r.Context(), idHash)
	if err != nil {
		return "", nil, false, err
	}
	if found && request.State == db.DeviceSignInApproved {
		return db.DeviceSignInExpired, struct{}{}, true, nil
	}
	return settleDeviceSignIn(r, database, idHash, request, found)
}

func writeDeviceSignInEvent(w http.ResponseWriter, event string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		body = []byte("{}")
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body)
}
