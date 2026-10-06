package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// A2A push notifications never leave Misty. A push config's url must be the
// member's Misty A2A inbox (/a2a/push); Misty never calls other addresses, so
// there is no webhook to point at an internal service or a third party. The
// agent reads its inbox as a server-sent event stream with the same session
// and agent token as every other A2A call. Each state change is recorded
// before it is written and released if the write fails, so it reaches one
// open inbox, or the next one to connect.
var (
	a2aInboxLifetime = 30 * time.Minute
	a2aInboxSweep    = 30 * time.Second
	a2aPushConfigID  = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,100}$`)
)

const a2aInboxBatch = 100

type a2aPushConfigInput struct {
	ID             string          `json:"id"`
	URL            string          `json:"url"`
	Token          string          `json:"token"`
	Authentication json.RawMessage `json:"authentication"`
}

func a2aInboxPath(r *http.Request) string {
	path := r.URL.Path
	if index := strings.Index(path, "/a2a/"); index >= 0 {
		return path[:index] + "/a2a/push"
	}
	return "/a2a/push"
}

// validateA2APushConfig accepts only the member's own Misty inbox.
func validateA2APushConfig(r *http.Request, config *a2aPushConfigInput) error {
	if config.ID != "" && !a2aPushConfigID.MatchString(config.ID) {
		return a2aFail(a2aInvalidParams, "Push notification config ids use letters, digits and . _ : - (at most 100)")
	}
	if len(config.Token) > 512 {
		return a2aFail(a2aInvalidParams, "Push notification tokens are at most 512 characters")
	}
	if len(config.Authentication) > 0 && string(config.Authentication) != "null" {
		return a2aFail(a2aPushNotSupported, "The Misty inbox uses your session and agent token; push authentication is not accepted")
	}
	inbox := a2aInboxPath(r)
	target, err := url.Parse(strings.TrimSpace(config.URL))
	if err != nil || target.Path != inbox || target.RawQuery != "" || target.Fragment != "" || target.User != nil || target.Opaque != "" {
		return a2aFail(a2aPushNotSupported, "Push notifications go only to your Misty A2A inbox: "+a2aPublicURL(inbox))
	}
	if target.Host != "" {
		public, _ := url.Parse(a2aPublicURL(inbox))
		if public == nil || public.Host == "" || !strings.EqualFold(target.Host, public.Host) || target.Scheme != public.Scheme {
			return a2aFail(a2aPushNotSupported, "Push notifications go only to your Misty A2A inbox: "+a2aPublicURL(inbox))
		}
	}
	return nil
}

func a2aNewPushConfigID() string {
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return "push_" + base64.RawURLEncoding.EncodeToString(buf)
}

func a2aPushConfigView(r *http.Request, config db.A2APushConfig) map[string]any {
	push := map[string]any{"id": config.ID, "url": a2aPublicURL(a2aInboxPath(r))}
	if config.Token != "" {
		push["token"] = config.Token
	}
	return map[string]any{"taskId": config.RequestID, "pushNotificationConfig": push}
}

func (s *SpacesService) setA2APushConfig(r *http.Request, caller a2aCaller, taskID string, input *a2aPushConfigInput) (db.A2APushConfig, error) {
	id := input.ID
	if id == "" {
		id = a2aNewPushConfigID()
	}
	config, err := s.database.SetA2APushConfig(r.Context(), caller.UserID, caller.AgentID, taskID, id, input.Token)
	if errors.Is(err, db.ErrAgentRequestLimit) {
		return config, a2aFail(a2aInvalidParams, "A task takes at most 5 push notification configs")
	}
	return config, err
}

// a2aPushConfigCall handles tasks/pushNotificationConfig/{set,get,list,delete}.
func (s *SpacesService) a2aPushConfigCall(r *http.Request, caller a2aCaller, listing *db.SpaceAgentListing, method string, raw json.RawMessage) (any, error) {
	var params struct {
		TaskID                   string              `json:"taskId"`
		ID                       string              `json:"id"`
		PushNotificationConfigID string              `json:"pushNotificationConfigId"`
		PushNotificationConfig   *a2aPushConfigInput `json:"pushNotificationConfig"`
	}
	if json.Unmarshal(raw, &params) != nil {
		return nil, db.ErrSpaceInvalid
	}
	taskID := params.ID
	if method == "tasks/pushNotificationConfig/set" {
		taskID = params.TaskID
	}
	if strings.TrimSpace(taskID) == "" {
		return nil, db.ErrSpaceInvalid
	}
	request, err := s.a2aOwnTask(r, caller, listing, taskID)
	if err != nil {
		return nil, err
	}
	switch method {
	case "tasks/pushNotificationConfig/set":
		if params.PushNotificationConfig == nil {
			return nil, db.ErrSpaceInvalid
		}
		if err := validateA2APushConfig(r, params.PushNotificationConfig); err != nil {
			return nil, err
		}
		config, err := s.setA2APushConfig(r, caller, request.ID, params.PushNotificationConfig)
		if err != nil {
			return nil, err
		}
		return a2aPushConfigView(r, config), nil
	case "tasks/pushNotificationConfig/delete":
		if params.PushNotificationConfigID == "" {
			return nil, db.ErrSpaceInvalid
		}
		return nil, s.database.DeleteA2APushConfig(r.Context(), caller.UserID, caller.AgentID, request.ID, params.PushNotificationConfigID)
	}
	configs, err := s.database.A2APushConfigs(r.Context(), caller.UserID, caller.AgentID, request.ID, params.PushNotificationConfigID)
	if err != nil {
		return nil, err
	}
	if method == "tasks/pushNotificationConfig/get" {
		if len(configs) == 0 {
			return nil, a2aFail(a2aInvalidParams, "No push notification config on this task")
		}
		return a2aPushConfigView(r, configs[0]), nil
	}
	views := make([]map[string]any, 0, len(configs))
	for _, config := range configs {
		views = append(views, a2aPushConfigView(r, config))
	}
	return views, nil
}

// A2APushInbox streams push notifications for the calling agent's tasks.
func (s *SpacesService) A2APushInbox() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, ok := s.a2aAuthenticate(w, r)
		if !ok {
			return
		}
		if !a2aStreamHeaders(w) {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "streaming_unavailable"})
			return
		}
		events, unsubscribe, err := s.database.SubscribeAccountEvents(r.Context(), caller.UserID)
		if err == nil {
			defer unsubscribe()
		}
		if s.deliverA2APush(w, r, caller, "") != nil {
			return
		}
		sweep := time.NewTicker(a2aInboxSweep)
		defer sweep.Stop()
		check := time.NewTicker(a2aStreamCheck)
		defer check.Stop()
		keepalive := time.NewTicker(a2aStreamKeepalive)
		defer keepalive.Stop()
		lifetime := time.NewTimer(a2aInboxLifetime)
		defer lifetime.Stop()
		for {
			var deliverErr error
			select {
			case <-r.Context().Done():
				return
			case <-lifetime.C:
				return
			case <-keepalive.C:
				deliverErr = writeAIInvocationSSE(w, ": keep-alive\n\n")
			case <-check.C:
				if authorized, err := s.a2aStillAuthorized(r, caller); err == nil && !authorized {
					return
				}
			case event := <-events:
				switch {
				case event.Topic == "reset":
					deliverErr = s.deliverA2APush(w, r, caller, "")
				case event.Topic == "agent_requests" && event.ID != "":
					deliverErr = s.deliverA2APush(w, r, caller, event.ID)
				}
			case <-sweep.C:
				deliverErr = s.deliverA2APush(w, r, caller, "")
			}
			if deliverErr != nil {
				return
			}
		}
	}
}

// deliverA2APush writes each config whose task changed since its last
// delivery. requestID narrows it to one task. Only a failed write is an
// error; a database hiccup waits for the next sweep.
func (s *SpacesService) deliverA2APush(w http.ResponseWriter, r *http.Request, caller a2aCaller, requestID string) error {
	configs, err := s.database.PendingA2APushConfigs(r.Context(), caller.UserID, caller.AgentID, requestID, a2aInboxBatch)
	if err != nil {
		return nil
	}
	tasks := map[string]*db.AgentMemberRequest{}
	for _, config := range configs {
		request, seen := tasks[config.RequestID]
		if !seen {
			request, _ = s.database.A2AAgentRequest(r.Context(), caller.UserID, caller.AgentID, config.RequestID)
			tasks[config.RequestID] = request
		}
		if request == nil {
			continue
		}
		state, _, _ := a2aTaskStatus(request)
		if state == config.DeliveredState {
			continue
		}
		claimed, err := s.database.ClaimA2APushDelivery(r.Context(), config, state)
		if err != nil || !claimed {
			continue
		}
		body, _ := json.Marshal(map[string]any{"kind": "push-notification", "pushNotificationConfigId": config.ID,
			"token": config.Token, "task": a2aTask(request)})
		if err := writeAIInvocationSSE(w, "event: push\ndata: "+string(body)+"\n\n"); err != nil {
			// Detached from the request so a closed connection still releases.
			_ = s.database.ReleaseA2APushDelivery(context.WithoutCancel(r.Context()), config, state)
			return err
		}
	}
	return nil
}
