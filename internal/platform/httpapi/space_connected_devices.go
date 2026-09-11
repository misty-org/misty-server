package api

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"net/http"
	"time"
)

func writeSpacePeerError(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrSpaceForbidden) || errors.Is(err, db.ErrAppRuntimeForbidden) || errors.Is(err, db.ErrAppNotInstalled) {
		http.Error(w, "Files access is unavailable in this Space", http.StatusForbidden)
		return
	}
	writeAgentError(w, err)
}
func (s *AgentsService) UpdateSpaceConnectedDevicePresence() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok || !s.requireConnectedDevices(w) {
			return
		}
		var body db.SpaceDevicePresence
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if (body.SpaceID != "" && body.SpaceID != spaceID) || !p2pEndpointIDPattern.MatchString(body.EndpointID) || body.ProtocolVersion != db.SpacePeerProtocol || body.InstalledVersion == "" || len(body.InstalledVersion) > 128 || body.AuthorityGeneration < 1 || (body.ConnectionHint != "unknown" && body.ConnectionHint != "direct" && body.ConnectionHint != "relay") || !validJSONObject(body.Addressing) || containsLocalPath(body.Addressing) || containsClipboardValue(body.Addressing) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		body.SpaceID = spaceID
		if err := s.database.UpdateSpaceDevicePresence(r.Context(), userID, chi.URLParam(r, "deviceID"), body); err != nil {
			writeSpacePeerError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"spaceId": spaceID, "authorityGeneration": body.AuthorityGeneration})
	}
}
func (s *AgentsService) ListSpaceConnectedDevicePeers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok || !s.requireConnectedDevices(w) {
			return
		}
		peers, err := s.database.ConnectedSpacePeers(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "deviceID"))
		if err != nil {
			writeSpacePeerError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"peers": peers})
	}
}
func (s *AgentsService) IssueSpaceConnectedDeviceTicket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok || !s.requireConnectedDevices(w) {
			return
		}
		var body struct {
			TargetDeviceID  string `json:"targetDeviceId"`
			ProtocolVersion string `json:"protocolVersion"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		if !deviceIDPattern.MatchString(body.TargetDeviceID) || body.ProtocolVersion != db.SpacePeerProtocol {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		subject, err := s.database.SpacePeerTicketSubject(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "deviceID"), body.TargetDeviceID)
		if err != nil {
			writeSpacePeerError(w, err)
			return
		}
		now := time.Now().UTC()
		claims := connectedDeviceTicketClaims{
			Issuer: connectedDeviceTicketIssuer, Audience: db.SpacePeerProtocol, JTI: "peerticket_" + uuid.NewString(),
			PairID: subject.PairID, SourceDeviceID: subject.SourceDeviceID, SourceEndpointID: subject.SourceEndpointID,
			TargetDeviceID: subject.TargetDeviceID, TargetEndpointID: subject.TargetEndpointID,
			ProtocolVersion: db.SpacePeerProtocol, Permissions: []string{"roots:read", "files:read", "directories:subscribe"},
			IssuedAt: now.Unix(), Expires: now.Add(connectedDeviceTicketLifetime).Unix(),
			SpaceID: subject.SpaceID, AppID: "files", InstalledVersion: subject.InstalledVersion, AuthorityGeneration: subject.AuthorityGeneration,
		}
		ticket, err := s.signConnectedDeviceTicket(claims)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ticket": ticket, "keyId": s.connectedDevices.KeyID, "expiresAt": time.Unix(claims.Expires, 0).UTC()})
	}
}
