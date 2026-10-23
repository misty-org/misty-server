package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const slackAPI = "https://slack.com/api/"

// slackGet reads a Slack Web API method; Slack reports errors in the body.
func slackGet(call inboundCall, method string, query url.Values, out any) error {
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	result, err := call("GET", slackAPI+method+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	if json.Unmarshal(result.Data, &envelope) != nil {
		return fmt.Errorf("Slack answered %d", result.Status)
	}
	if !envelope.OK {
		if envelope.Error == "missing_scope" || envelope.Error == "not_allowed_token_type" || envelope.Error == "invalid_auth" {
			return errInboundScope
		}
		return errors.New("Slack said " + envelope.Error)
	}
	return json.Unmarshal(result.Data, out)
}

func slackUser(call inboundCall) (userID, teamID string, err error) {
	var who struct {
		UserID string `json:"user_id"`
		TeamID string `json:"team_id"`
	}
	if err := slackGet(call, "auth.test", url.Values{}, &who); err != nil {
		return "", "", err
	}
	if who.UserID == "" {
		return "", "", errors.New("Slack didn't say who is connected")
	}
	return who.UserID, who.TeamID, nil
}

var slackMention = regexp.MustCompile(`(?i)(^|[^\w@])@misty\b[:,]?`)

// slackNewMessages finds messages the person wrote that mention @misty, since
// after. Other people's messages never start tasks.
func slackNewMessages(call inboundCall, userID string, after time.Time) ([]inboundMessage, error) {
	var found struct {
		Messages struct {
			Matches []struct {
				TS        string `json:"ts"`
				Text      string `json:"text"`
				User      string `json:"user"`
				Permalink string `json:"permalink"`
				Channel   struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"channel"`
			} `json:"matches"`
		} `json:"messages"`
	}
	query := url.Values{"query": {"@misty from:<@" + userID + ">"}, "sort": {"timestamp"}, "sort_dir": {"desc"}, "count": {"20"}}
	if err := slackGet(call, "search.messages", query, &found); err != nil {
		return nil, err
	}
	items := []inboundMessage{}
	for _, match := range found.Messages.Matches {
		seconds, err := strconv.ParseFloat(match.TS, 64)
		if err != nil || match.User != userID || match.Channel.ID == "" || seconds <= float64(after.Unix()) || !slackMention.MatchString(match.Text) {
			continue
		}
		instruction := strings.TrimSpace(slackMention.ReplaceAllString(match.Text, " "))
		if instruction == "" {
			continue
		}
		thread := match.TS
		if link, err := url.Parse(match.Permalink); err == nil && link.Query().Get("thread_ts") != "" {
			thread = link.Query().Get("thread_ts")
		}
		title := instruction
		if len([]rune(title)) > 60 {
			title = string([]rune(title)[:60])
		}
		items = append(items, inboundMessage{
			ExternalID: match.Channel.ID + ":" + match.TS, Title: title, Instruction: instruction,
			ThreadRef: map[string]any{"channel": match.Channel.ID, "channel_name": match.Channel.Name, "thread_ts": thread},
		})
	}
	return items, nil
}

// slackReply answers in the message's thread. It never repeats "@misty", so
// the answer can't start another task.
func slackReply(call inboundCall, ref map[string]any, text string) error {
	channel, _ := ref["channel"].(string)
	thread, _ := ref["thread_ts"].(string)
	text = slackMention.ReplaceAllString(text, "$1Misty")
	result, err := call("POST", slackAPI+"chat.postMessage", map[string]any{
		"channel": channel, "thread_ts": thread, "text": text, "unfurl_links": false,
	})
	if err != nil {
		return err
	}
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if json.Unmarshal(result.Data, &envelope) != nil || !envelope.OK {
		return errors.New("Slack didn't post the answer: " + envelope.Error)
	}
	return nil
}
