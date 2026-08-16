package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	mcpintegration "github.com/kannachi323/misty/server/internal/integrations/mcp"
	mistyemail "github.com/kannachi323/misty/server/internal/platform/email"
)

type SpacesService struct {
	TestingJournalCollab     JournalCollabConfig
	database                 *db.Database
	agent                    *serveragent.Service
	library                  *SpaceLibraryService
	avatarStore              LibraryObjectStore
	aead                     cipher.AEAD
	keyVer                   int16
	workers                  sync.Once
	invitationSender         mistyemail.SpaceInvitationSender
	invitationBaseURL        string
	agentRuntime             AgentRuntimeConfig
	usageMeter               serveragent.UsageMeter
	mailProviderFactory      MailProviderFactory
	slackChatProviderFactory SlackChatProviderFactory
	githubAppProviderFactory GitHubAppProviderFactory
	figmaProviderFactory     FigmaProviderFactory
	mcpConnectorClient       mcpintegration.ConnectorClient
}

func (s *SpacesService) TestingSetFigmaProviderFactory(factory FigmaProviderFactory) {
	s.figmaProviderFactory = factory
}
func (s *SpacesService) figmaProvider(token string) FigmaProvider {
	if s.figmaProviderFactory != nil {
		return s.figmaProviderFactory(token)
	}
	return newFigmaClient(token)
}

func (s *SpacesService) SetAgentRuntime(config AgentRuntimeConfig) {
	s.agentRuntime = config
}

func (s *SpacesService) SetUsageMeter(meter serveragent.UsageMeter) {
	s.usageMeter = meter
}

func (s *SpacesService) SetInvitationSender(
	sender mistyemail.SpaceInvitationSender,
	baseURL string,
) {
	s.invitationSender = sender
	s.invitationBaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func NewSpacesService(database *db.Database, agent *serveragent.Service, encryptionKey string) (*SpacesService, error) {
	key, err := parseSpaceEncryptionKey(encryptionKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SpacesService{database: database, agent: agent, aead: aead, keyVer: 1,
		mailProviderFactory: defaultMailProviderFactory, slackChatProviderFactory: defaultSlackChatProviderFactory,
		mcpConnectorClient: mcpintegration.NewClient(mcpintegration.DefaultLimits())}, nil
}

func (s *SpacesService) TestingSetMCPConnectorClient(client mcpintegration.ConnectorClient) {
	s.mcpConnectorClient = client
}

func (s *SpacesService) TestingSetGitHubAppProviderFactory(factory GitHubAppProviderFactory) {
	s.githubAppProviderFactory = factory
}

func (s *SpacesService) githubAppProvider(installationID int64) (GitHubAppProvider, error) {
	if s.githubAppProviderFactory != nil {
		provider := s.githubAppProviderFactory(installationID)
		if provider == nil {
			return nil, errors.New("github_app_not_configured")
		}
		return provider, nil
	}
	return newGitHubAppClient(installationID)
}

// SetLibraryProvider installs the server-side Library provider used by Agent
// workflow actions. Connections and authorization still come from the run's
// requesting user; the provider is only the byte/object transport.
func (s *SpacesService) SetLibraryProvider(library *SpaceLibraryService) {
	s.library = library
}

// SetAvatarStore installs the object store used to serve member avatars, which
// live under the avatars/ prefix in the shared bucket.
func (s *SpacesService) SetAvatarStore(store LibraryObjectStore) {
	s.avatarStore = store
}

func parseSpaceEncryptionKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("space link encryption key is required")
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if len(value) == 32 {
		return []byte(value), nil
	}
	return nil, errors.New("space link encryption key must be 32 bytes, base64, or hexadecimal")
}

func (s *SpacesService) TestingEncryptTarget(target string) ([]byte, []byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return s.aead.Seal(nil, nonce, []byte(target), []byte("misty-space-link-v1")), nonce, nil
}

func (s *SpacesService) TestingDecryptTarget(ciphertext, nonce []byte) (string, error) {
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, []byte("misty-space-link-v1"))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func TestingValidGoogleDriveTarget(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Host == "" {
		return nil, db.ErrSpaceInvalid
	}
	host := strings.ToLower(parsed.Hostname())
	allowed := host == "drive.google.com" || host == "docs.google.com" || host == "drive.usercontent.google.com" || host == "lh3.googleusercontent.com" || strings.HasSuffix(host, ".googleusercontent.com")
	if !allowed {
		return nil, db.ErrSpaceInvalid
	}
	parsed.Fragment = ""
	return parsed, nil
}

func authenticatedUser(w http.ResponseWriter, r *http.Request, database *db.Database) (string, bool) {
	userID, err := sessionUserID(r, database)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
		return "", false
	}
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "not_authenticated"})
		return "", false
	}
	return userID, true
}

func (s *SpacesService) MemberAvatar() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestingUserID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		memberID := chi.URLParam(r, "userID")
		version, err := s.database.SpaceMemberAvatarMeta(r.Context(), requestingUserID, spaceID, memberID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if version == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		// Authorization was checked above; stream the bytes from the object store.
		serveAvatarObject(w, r, s.avatarStore, memberID, version)
	}
}

func writeSpaceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrSpaceNotFound), errors.Is(err, db.ErrSpaceInviteNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
	case errors.Is(err, db.ErrPersonalAgentNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "agent_not_found"})
	case errors.Is(err, db.ErrSpaceInviteeNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "invitee_not_found"})
	case errors.Is(err, db.ErrSpaceForbidden), errors.Is(err, db.ErrLibraryForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "forbidden"})
	case errors.Is(err, db.ErrWorkflowIntegrationRequired):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "integration_required"})
	case errors.Is(err, db.ErrSpaceLimit):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "space_limit_reached"})
	case errors.Is(err, db.ErrSpaceOwnershipLimit):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "space_ownership_limit_reached"})
	case errors.Is(err, db.ErrSpacePeopleLimit):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "space_people_limit_reached"})
	case errors.Is(err, db.ErrSpaceNodeLimit):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "space_node_limit_reached"})
	case errors.Is(err, db.ErrLibraryQuota):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "owner_storage_quota_exceeded"})
	case errors.Is(err, db.ErrSpaceConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "version_conflict"})
	case errors.Is(err, db.ErrSpaceInviteExpired):
		writeJSON(w, http.StatusGone, map[string]string{"code": "invite_expired"})
	case errors.Is(err, db.ErrSpaceInvalid), errors.Is(err, db.ErrLibraryInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
	default:
		log.Printf("spaces API internal error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
	}
}

func (s *SpacesService) Spaces() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			if err := s.database.EnsureDefaultSpace(r.Context(), userID); err != nil {
				writeSpaceError(w, fmt.Errorf("ensure default Misty Space: %w", err))
				return
			}
			entitlements, err := s.database.EntitlementsForUser(r.Context(), userID)
			if err != nil {
				writeSpaceError(w, fmt.Errorf("load entitlements: %w", err))
				return
			}
			spaces, err := s.database.ListSpaces(r.Context(), userID)
			if err != nil {
				writeSpaceError(w, fmt.Errorf("list spaces: %w", err))
				return
			}
			invites, err := s.database.IncomingSpaceInvites(r.Context(), userID)
			if err != nil {
				writeSpaceError(w, fmt.Errorf("list incoming space invites: %w", err))
				return
			}
			storage, err := s.database.OwnerStorageUsage(r.Context(), userID)
			if err != nil {
				writeSpaceError(w, fmt.Errorf("load owner storage usage: %w", err))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"spaces": spaces, "invitations": invites,
				"entitlements":  entitlements,
				"owner_storage": storage})
		case http.MethodPost:
			var body struct {
				Name                 string   `json:"name"`
				TemplateID           string   `json:"template_id"`
				IntegrationProviders []string `json:"integration_providers"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			result, err := s.database.CreateSpaceWithTemplateIdempotent(
				r.Context(), userID, body.Name, body.TemplateID, body.IntegrationProviders,
				r.Header.Get("Idempotency-Key"),
			)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, result)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
