package api

import (
	"net/http"
	"regexp"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

// Discord's hard limit on message content. Mirrored text is trimmed, never
// dropped: losing the tail of someone's message is worse than an ellipsis.
const TestingDiscordContentLimit = 2000

// Discord message types Misty mirrors. 0 = default, 19 = inline reply.
var mirroredDiscordMessageTypes = map[int]bool{0: true, 19: true}

var discordUserMentionPattern = regexp.MustCompile(`<@!?(\d+)>`)

var discordChannelMentionPattern = regexp.MustCompile(`<#(\d+)>`)

var discordRoleMentionPattern = regexp.MustCompile(`<@&(\d+)>`)

func discordBotToken() string { return strings.TrimSpace(envconfig.Getenv("DISCORD_BOT_TOKEN")) }

// discordMessage is the subset of Discord's REST/Gateway message Misty reads.
type TestingDiscordMessage struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	GuildID   string `json:"guild_id"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Type      int    `json:"type"`
	WebhookID string `json:"webhook_id"`
	Author    struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
		Avatar     string `json:"avatar"`
		Bot        bool   `json:"bot"`
	} `json:"author"`
	Attachments []struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
		URL      string `json:"url"`
	} `json:"attachments"`
	Mentions []struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
	} `json:"mentions"`
	ReferencedMessage *struct {
		ID string `json:"id"`
	} `json:"referenced_message"`
}

// SpaceDiscordLink serves the Space's Discord links and creates or reconnects
// one channel link.
func (s *SpacesService) SpaceDiscordLink() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			links, err := s.database.SpaceDiscordLinksFor(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"links": links})
		case http.MethodPost:
			var body struct {
				IntegrationID string `json:"integration_id"`
				ChannelID     string `json:"channel_id"`
				ChannelName   string `json:"channel_name"`
				GuildID       string `json:"guild_id"`
				GuildName     string `json:"guild_name"`
				Direction     string `json:"direction"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			allowed, permissionErr := s.database.HasSpacePermission(r.Context(), userID, spaceID, db.PermissionIntegrationsManage)
			if permissionErr != nil || !allowed {
				writeSpaceError(w, db.ErrSpaceForbidden)
				return
			}
			credential, credentialErr := s.database.ProviderCredential(r.Context(), userID, spaceID, body.IntegrationID)
			if credentialErr != nil || credential.Provider != "discord" {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			if discordBotToken() == "" {
				writeJSON(w, http.StatusFailedDependency, map[string]string{"code": "provider_not_configured"})
				return
			}
			// Verify the bot can actually see the channel before storing a link
			// that would otherwise fail silently on every later sync.
			channel, err := s.discordChannel(r.Context(), body.ChannelID)
			if err != nil {
				writeProviderFailure(w, err)
				return
			}
			if body.GuildID != "" && body.GuildID != channel.GuildID {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			body.GuildID = channel.GuildID
			body.ChannelName = channel.Name
			guildName, guildErr := s.discordGuildName(r.Context(), channel.GuildID)
			if guildErr != nil {
				writeProviderFailure(w, guildErr)
				return
			}
			body.GuildName = guildName
			item := db.SpaceDiscordLink{
				SpaceID: spaceID, IntegrationID: body.IntegrationID,
				GuildID: body.GuildID, GuildName: body.GuildName, ChannelID: body.ChannelID,
				ChannelName: body.ChannelName, Direction: body.Direction,
			}
			if identity, identityErr := s.discordBotIdentity(r.Context()); identityErr == nil {
				item.BotUserID = identity
			}
			// A webhook lets each mirrored message post under its Misty author's
			// own name. It is optional: without Manage Webhooks the link still
			// works, posting as the bot with an attributed prefix.
			if webhookID, token, hookErr := s.createDiscordWebhook(r.Context(), body.ChannelID); hookErr == nil {
				if ciphertext, nonce, sealErr := s.encryptProviderSecret("discord", []byte(token)); sealErr == nil {
					item.WebhookID, item.WebhookCiphertext, item.WebhookNonce = webhookID, ciphertext, nonce
				}
			}
			link, err := s.database.CreateSpaceDiscordLink(r.Context(), userID, item)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			_ = s.database.SetSpaceSetupProviderStatus(r.Context(), userID, spaceID, "discord", "configured")
			// Backfill immediately so a freshly linked channel is not empty.
			if _, syncErr := s.syncDiscordLink(r.Context(), link); syncErr != nil {
				_ = s.database.SetSpaceDiscordLinkSync(r.Context(), link.ID, "", "needs_attention", providerErrorCode(syncErr), nil)
			}
			refreshed, err := s.database.SpaceDiscordLinkByID(r.Context(), spaceID, link.ID)
			if err != nil {
				writeJSON(w, http.StatusCreated, link)
				return
			}
			writeJSON(w, http.StatusCreated, refreshed)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// SpaceDiscordLinkItem changes the mirror direction or removes the link.
func (s *SpacesService) SpaceDiscordLinkItem() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, linkID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "linkID")
		switch r.Method {
		case http.MethodPatch:
			var body struct {
				Direction string `json:"direction"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			link, err := s.database.UpdateSpaceDiscordLinkDirection(r.Context(), userID, spaceID, linkID, body.Direction)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, link)
		case http.MethodDelete:
			allowed, permissionErr := s.database.HasSpacePermission(r.Context(), userID, spaceID, db.PermissionIntegrationsManage)
			if permissionErr != nil || !allowed {
				writeSpaceError(w, db.ErrSpaceForbidden)
				return
			}
			if link, linkErr := s.database.SpaceDiscordLinkByID(r.Context(), spaceID, linkID); linkErr == nil {
				_ = s.deleteDiscordWebhook(r.Context(), link)
			}
			if err := s.database.DeleteSpaceDiscordLink(r.Context(), userID, spaceID, linkID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// SyncSpaceDiscordLink pulls messages after the stored cursor. Safe to call
// repeatedly: the cursor plus the per-message uniqueness check make it
// idempotent, so a retry cannot duplicate a transcript.
func (s *SpacesService) SyncSpaceDiscordLink() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, linkID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "linkID")
		if _, err := s.database.SpaceDiscordLinkFor(r.Context(), userID, spaceID); err != nil {
			writeSpaceError(w, err)
			return
		}
		link, err := s.database.SpaceDiscordLinkByID(r.Context(), spaceID, linkID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		imported, syncErr := s.syncDiscordLink(r.Context(), link)
		if syncErr != nil {
			_ = s.database.SetSpaceDiscordLinkSync(r.Context(), link.ID, "", "needs_attention", providerErrorCode(syncErr), nil)
		}
		refreshed, err := s.database.SpaceDiscordLinkByID(r.Context(), spaceID, linkID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		response := map[string]any{"link": refreshed, "imported": imported, "skipped": 0}
		if syncErr != nil {
			response["error"] = discordFailureMessage(providerErrorCode(syncErr))
		}
		writeJSON(w, http.StatusOK, response)
	}
}
