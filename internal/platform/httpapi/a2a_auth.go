package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/accounts"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// Only members' own agents use the A2A endpoint, from inside Misty. Every
// call carries two credentials:
//
//   - the member's account session cookie, which proves who the member is;
//   - an agent token in the Misty-Agent-Token header, which proves which of
//     that member's agents is calling.
//
// The app mints agent tokens from the member's session for an agent the
// member owns. A token lasts five minutes, is bound to the session that minted
// it, and is re-checked against the agent on every call, so deleting or
// disabling the agent, or signing out, stops it. Neither credential works
// alone, and outside clients have no way to obtain either. The custom header
// also means a cross-site page cannot make these calls with the cookie.
const a2aAgentTokenHeader = "Misty-Agent-Token"

type a2aCaller struct {
	UserID      string
	AgentID     string
	SessionHash string
}

func a2aAgentTokens() (*security.AgentTokenSigner, error) {
	sessions, err := security.SessionSignerFromEnv()
	if err != nil {
		return nil, err
	}
	return sessions.AgentTokenSigner()
}

// A2AAgentToken mints an agent token for one of the signed-in member's agents.
func (s *SpacesService) A2AAgentToken() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		sessionID := accounts.SessionID(r)
		if sessionID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "not_authenticated"})
			return
		}
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		active, err := s.database.A2ARequesterAgentActive(r.Context(), userID, agentID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
			return
		}
		if !active {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "agent_not_found"})
			return
		}
		signer, err := a2aAgentTokens()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
			return
		}
		token, expires, err := signer.Mint(userID, agentID, sessionID, time.Now())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"token": token, "header": a2aAgentTokenHeader, "agent_id": agentID, "expires_at": expires})
	}
}

// a2aAuthenticate requires both the member's session and an agent token for
// one of that member's enabled agents. It writes the 401 itself.
func (s *SpacesService) a2aAuthenticate(w http.ResponseWriter, r *http.Request) (a2aCaller, bool) {
	userID, ok := authenticatedUser(w, r, s.database)
	if !ok {
		return a2aCaller{}, false
	}
	deny := func() (a2aCaller, bool) {
		w.Header().Set("WWW-Authenticate", `MistyAgent realm="misty-a2a"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "agent_token_required"})
		return a2aCaller{}, false
	}
	sessionID := accounts.SessionID(r)
	signer, err := a2aAgentTokens()
	if err != nil || sessionID == "" {
		return deny()
	}
	claims, err := signer.Verify(strings.TrimSpace(r.Header.Get(a2aAgentTokenHeader)), userID, sessionID)
	if err != nil {
		return deny()
	}
	active, err := s.database.A2ARequesterAgentActive(r.Context(), userID, claims.AgentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
		return a2aCaller{}, false
	}
	if !active {
		return deny()
	}
	return a2aCaller{UserID: userID, AgentID: claims.AgentID, SessionHash: security.HashToken(sessionID)}, true
}

// a2aStillAuthorized re-checks a long-lived stream's caller: the session is
// still signed in and the agent is still the member's.
func (s *SpacesService) a2aStillAuthorized(r *http.Request, caller a2aCaller) (bool, error) {
	active, err := s.database.AccountSessionActive(r.Context(), caller.SessionHash, caller.UserID)
	if err != nil || !active {
		return active, err
	}
	return s.database.A2ARequesterAgentActive(r.Context(), caller.UserID, caller.AgentID)
}
