package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/integrations/composio"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// Give the agent a task by email or Slack (roadmap C8). With a channel on,
// email the person forwards to their +misty address, or a Slack message they
// write that mentions @misty, starts a task with Misty. It runs like any
// other task, on their models, and the answer goes back to the same thread.

// inboundTaskTrigger marks invocations an email or Slack message started.
const inboundTaskTrigger = "inbound_task"

const inboundReplyLimit = 3500

var inboundToolkits = map[string]string{"gmail": "gmail", "slack": "slack"}

func inboundCaller(ctx context.Context, client *composio.Client, account string) inboundCall {
	return func(method, address string, body any) (composio.ProxyResult, error) {
		return client.ProxyURL(ctx, account, method, address, body)
	}
}

// inboundAccount is the person's active connection for a channel's app.
func inboundAccount(ctx context.Context, client *composio.Client, userID, channel string) (string, error) {
	accounts, err := client.Accounts(ctx, composio.UserID(userID), inboundToolkits[channel])
	if err != nil {
		return "", err
	}
	for _, account := range accounts {
		if account.Active() {
			return account.ID, nil
		}
	}
	return "", errInboundNotConnected
}

var errInboundNotConnected = errors.New("not connected")

func inboundAppName(channel string) string {
	if channel == "slack" {
		return "Slack"
	}
	return "Gmail"
}

// inboundFailure says what went wrong in plain words for Settings.
func inboundFailure(channel string, err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errInboundNotConnected):
		return inboundAppName(channel) + " isn't connected any more. Connect it again in Agents › Integrations."
	case errors.Is(err, errInboundScope):
		return inboundAppName(channel) + " didn't allow Misty to read your messages. Connect it again in Agents › Integrations and allow reading and sending messages."
	default:
		return "Misty couldn't check " + inboundAppName(channel) + " just now. It keeps trying every minute."
	}
}

// InboundChannels lists the account's email and Slack task channels.
func (s *AIService) InboundChannels() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		channels, err := s.database.UserInboundChannels(r.Context(), userID)
		if err != nil {
			WriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"channels": channels})
	}
}

// InboundChannel turns a channel on (PUT) or off (DELETE). Turning it on
// finds the person's connection and who they are there.
func (s *AIService) InboundChannel() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		channel := chi.URLParam(r, "channel")
		if !db.InboundChannels[channel] {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "unknown_channel"})
			return
		}
		if r.Method == http.MethodDelete {
			if err := s.database.DisableInboundChannel(r.Context(), userID, channel); err != nil {
				WriteAIError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		client, err := composioClient()
		if err != nil {
			writeAppsError(w, err)
			return
		}
		account, err := inboundAccount(r.Context(), client, userID, channel)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "app_not_connected", "message": inboundFailure(channel, errInboundNotConnected)})
			return
		}
		call := inboundCaller(r.Context(), client, account)
		identity := map[string]any{}
		if channel == "gmail" {
			address, err := gmailAddress(call)
			if err != nil {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "app_unavailable", "message": inboundFailure(channel, err)})
				return
			}
			identity["address"], identity["task_address"] = address, gmailTaskAddress(address)
		} else {
			user, team, err := slackUser(call)
			if err != nil {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "app_unavailable", "message": inboundFailure(channel, err)})
				return
			}
			identity["user_id"], identity["team_id"] = user, team
		}
		raw, _ := json.Marshal(identity)
		saved, err := s.database.EnableInboundChannel(r.Context(), userID, channel, account, raw)
		if err != nil {
			WriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"channel": saved})
	}
}

// ProcessInboundTasks checks the channels that are due and starts a task for
// each new message the person wrote to Misty.
func (s *AIService) ProcessInboundTasks(ctx context.Context, now time.Time, limit int) (int, error) {
	channels, err := s.database.ClaimDueInboundChannels(ctx, now, limit)
	if err != nil || len(channels) == 0 {
		return 0, err
	}
	client, err := composioClient()
	if err != nil {
		return 0, nil // Apps are off on this server; channels wait.
	}
	started := 0
	for _, channel := range channels {
		count, checkErr := s.checkInboundChannel(ctx, client, channel)
		started += count
		if err := s.database.FinishInboundCheck(ctx, channel.UserID, channel.Channel, inboundFailure(channel.Channel, checkErr)); err != nil {
			return started, err
		}
	}
	return started, nil
}

func (s *AIService) checkInboundChannel(ctx context.Context, client *composio.Client, channel db.InboundChannel) (int, error) {
	var identity map[string]string
	_ = json.Unmarshal(channel.Identity, &identity)
	call := inboundCaller(ctx, client, channel.ConnectedAccountID)
	var messages []inboundMessage
	var err error
	if channel.Channel == "gmail" {
		messages, err = gmailNewMessages(call, identity["task_address"], channel.WatchAfter)
	} else {
		messages, err = slackNewMessages(call, identity["user_id"], channel.WatchAfter)
	}
	if err != nil {
		return 0, err
	}
	started := 0
	for _, message := range messages {
		if err := s.startInboundTask(ctx, channel, message); err != nil {
			log.Printf("inbound task for %s: %v", channel.Channel, err)
			continue
		}
		started++
	}
	return started, nil
}
