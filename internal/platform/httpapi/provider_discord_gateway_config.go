package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	"github.com/gorilla/websocket"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const discordGatewayIntents = 1 | 512 | 1024 | 32768 // guilds, messages, reactions, message content

func (s *SpacesService) StartProviderWorkers(ctx context.Context) {
	s.workers.Do(func() {
		go s.runWorkflowCoordinator(ctx)
		if strings.TrimSpace(envconfig.Getenv("DISCORD_BOT_TOKEN")) != "" {
			go s.runDiscordGateway(ctx)
		}
	})
}

func (s *SpacesService) runWorkflowCoordinator(ctx context.Context) {
	run := func() {
		iteration, cancel := context.WithTimeout(ctx, 50*time.Second)
		defer cancel()
		_, _ = s.ProcessDueAgentWorkflows(iteration, time.Now().UTC(), 100)
		_, _ = s.ReconcileGoogleCalendars(iteration, 100)
	}
	run()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

type discordGatewayEnvelope struct {
	Operation int             `json:"op"`
	Data      json.RawMessage `json:"d"`
	Sequence  *int64          `json:"s"`
	Type      string          `json:"t"`
}

func (s *SpacesService) runDiscordGateway(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		state, err := s.database.ProviderGatewayState(ctx, "discord")
		if err == nil {
			err = s.discordGatewaySession(ctx, state)
		}
		if ctx.Err() != nil {
			return
		}
		if state == nil {
			state = &db.ProviderGatewayState{Provider: "discord"}
		}
		wasConnected := state.Status == "connected"
		state.Status, state.LastErrorCode = "disconnected", providerErrorCode(err)
		_ = s.database.SaveProviderGatewayState(context.Background(), *state, false, false)
		if wasConnected {
			backoff = time.Second
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func (s *SpacesService) discordGatewaySession(ctx context.Context, state *db.ProviderGatewayState) error {
	token := strings.TrimSpace(envconfig.Getenv("DISCORD_BOT_TOKEN"))
	endpoint := "wss://gateway.discord.gg/?v=10&encoding=json"
	if state.ResumeURL != "" && state.SessionID != "" {
		endpoint = strings.TrimRight(state.ResumeURL, "/") + "/?v=10&encoding=json"
	}
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, endpoint, http.Header{"User-Agent": {"Misty Space Agents/1.0"}})
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		return err
	}
	defer conn.Close()
	conn.SetReadLimit(providerCallbackBodyLimit)
	_, helloRaw, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	var hello discordGatewayEnvelope
	var helloData struct {
		HeartbeatInterval int64 `json:"heartbeat_interval"`
	}
	if json.Unmarshal(helloRaw, &hello) != nil || hello.Operation != 10 || json.Unmarshal(hello.Data, &helloData) != nil || helloData.HeartbeatInterval < 1000 {
		return errors.New("discord gateway returned an invalid hello")
	}
	var writer sync.Mutex
	writeJSON := func(value any) error {
		writer.Lock()
		defer writer.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(value)
	}
	var sequence atomic.Int64
	sequence.Store(state.Sequence)
	var acknowledged atomic.Bool
	acknowledged.Store(true)
	heartbeatDone := make(chan struct{})
	defer close(heartbeatDone)
	interval := time.Duration(helloData.HeartbeatInterval) * time.Millisecond
	go func() {
		jitter := time.Duration(time.Now().UnixNano()%helloData.HeartbeatInterval) * time.Millisecond
		timer := time.NewTimer(jitter)
		defer timer.Stop()
		select {
		case <-heartbeatDone:
			return
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if !acknowledged.Swap(false) {
				_ = conn.Close()
				return
			}
			seq := sequence.Load()
			var data any = seq
			if seq == 0 {
				data = nil
			}
			if writeJSON(map[string]any{"op": 1, "d": data}) != nil {
				return
			}
			heartbeatState := *state
			heartbeatState.Sequence, heartbeatState.Status, heartbeatState.LastErrorCode = seq, "connected", ""
			_ = s.database.SaveProviderGatewayState(context.Background(), heartbeatState, true, false)
			select {
			case <-heartbeatDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	if state.SessionID != "" && state.ResumeURL != "" {
		err = writeJSON(map[string]any{"op": 6, "d": map[string]any{"token": token, "session_id": state.SessionID, "seq": state.Sequence}})
	} else {
		err = writeJSON(map[string]any{"op": 2, "d": map[string]any{"token": token, "intents": discordGatewayIntents, "properties": map[string]string{"os": "linux", "browser": "misty", "device": "misty"}}})
	}
	if err != nil {
		return err
	}
	for ctx.Err() == nil {
		_ = conn.SetReadDeadline(time.Now().Add(interval*2 + 15*time.Second))
		_, raw, readErr := conn.ReadMessage()
		if readErr != nil {
			return readErr
		}
		var envelope discordGatewayEnvelope
		if json.Unmarshal(raw, &envelope) != nil {
			continue
		}
		if envelope.Sequence != nil {
			sequence.Store(*envelope.Sequence)
			state.Sequence = *envelope.Sequence
		}
		switch envelope.Operation {
		case 0:
			if envelope.Type == "READY" {
				var ready struct {
					SessionID        string `json:"session_id"`
					ResumeGatewayURL string `json:"resume_gateway_url"`
				}
				_ = json.Unmarshal(envelope.Data, &ready)
				state.SessionID, state.ResumeURL = ready.SessionID, ready.ResumeGatewayURL
			}
			state.Status, state.LastErrorCode = "connected", ""
			_ = s.database.SaveProviderGatewayState(ctx, *state, false, true)
			if err := s.processDiscordDispatch(ctx, envelope, raw); err != nil {
				// A malformed or unselected event must not terminate the Gateway.
				continue
			}
		case 1:
			seq := sequence.Load()
			_ = writeJSON(map[string]any{"op": 1, "d": seq})
		case 7:
			return errors.New("discord requested reconnect")
		case 9:
			var resumable bool
			_ = json.Unmarshal(envelope.Data, &resumable)
			if !resumable {
				state.SessionID, state.ResumeURL, state.Sequence = "", "", 0
				_ = s.database.SaveProviderGatewayState(ctx, *state, false, false)
			}
			return errors.New("discord session invalid")
		case 11:
			acknowledged.Store(true)
		}
	}
	return ctx.Err()
}
