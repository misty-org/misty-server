package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type SlackChatIdentity struct {
	TeamID, TeamName, UserID string
}

type SlackChatChannel struct {
	ID, Name string
}

type SlackChatFile struct {
	Name, URL string
}

type SlackChatMessage struct {
	Type, Subtype, UserID, BotID, Text, Timestamp, ThreadTimestamp string
	ReplyCount                                                     int
	Files                                                          []SlackChatFile
	Deleted                                                        bool
}

type SlackChatPage struct {
	Messages   []SlackChatMessage
	NextCursor string
}

type SlackChatProvider interface {
	Identity(context.Context) (SlackChatIdentity, error)
	Channel(context.Context, string) (SlackChatChannel, error)
	History(context.Context, string, string, string) (SlackChatPage, error)
	Replies(context.Context, string, string) ([]SlackChatMessage, error)
	Post(context.Context, string, string, string, string) (string, error)
}

type SlackChatProviderFactory func(string, string) SlackChatProvider

type slackWebClient struct {
	token, tokenType string
}

func defaultSlackChatProviderFactory(token, tokenType string) SlackChatProvider {
	return &slackWebClient{token: token, tokenType: tokenType}
}

func (s *SpacesService) TestingSetSlackChatProviderFactory(factory SlackChatProviderFactory) {
	s.slackChatProviderFactory = factory
}

func (c *slackWebClient) get(ctx context.Context, method string, query url.Values, out any) error {
	raw, err := providerJSONRequest(ctx, c.token, c.tokenType, http.MethodGet,
		"https://slack.com/api/"+method+"?"+query.Encode(), nil, nil)
	if err != nil {
		return err
	}
	if json.Unmarshal(raw, out) != nil {
		return errors.New("slack returned invalid JSON")
	}
	return nil
}

func (c *slackWebClient) Identity(ctx context.Context) (SlackChatIdentity, error) {
	var result struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		TeamID string `json:"team_id"`
		Team   string `json:"team"`
		UserID string `json:"user_id"`
	}
	if err := c.get(ctx, "auth.test", nil, &result); err != nil {
		return SlackChatIdentity{}, err
	}
	if !result.OK || result.TeamID == "" || result.UserID == "" {
		return SlackChatIdentity{}, errors.New("slack identity unavailable: " + result.Error)
	}
	return SlackChatIdentity{TeamID: result.TeamID, TeamName: result.Team, UserID: result.UserID}, nil
}

func (c *slackWebClient) Channel(ctx context.Context, channelID string) (SlackChatChannel, error) {
	var result struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
		Channel struct {
			ID, Name string
		} `json:"channel"`
	}
	if err := c.get(ctx, "conversations.info", url.Values{"channel": {channelID}}, &result); err != nil {
		return SlackChatChannel{}, err
	}
	if !result.OK || result.Channel.ID == "" {
		return SlackChatChannel{}, errors.New("slack channel unavailable: " + result.Error)
	}
	return SlackChatChannel{ID: result.Channel.ID, Name: result.Channel.Name}, nil
}

type slackMessageWire struct {
	Type       string `json:"type"`
	Subtype    string `json:"subtype"`
	User       string `json:"user"`
	BotID      string `json:"bot_id"`
	Text       string `json:"text"`
	TS         string `json:"ts"`
	ThreadTS   string `json:"thread_ts"`
	ReplyCount int    `json:"reply_count"`
	Files      []struct {
		Name       string `json:"name"`
		PrivateURL string `json:"url_private"`
		Permalink  string `json:"permalink"`
	} `json:"files"`
}

func normalizeSlackMessage(value slackMessageWire) SlackChatMessage {
	result := SlackChatMessage{Type: value.Type, Subtype: value.Subtype, UserID: value.User,
		BotID: value.BotID, Text: value.Text, Timestamp: value.TS,
		ThreadTimestamp: value.ThreadTS, ReplyCount: value.ReplyCount}
	for _, file := range value.Files {
		result.Files = append(result.Files, SlackChatFile{Name: file.Name,
			URL: firstNonempty(file.Permalink, file.PrivateURL)})
	}
	return result
}

func (c *slackWebClient) History(ctx context.Context, channelID, oldest, cursor string) (SlackChatPage, error) {
	query := url.Values{"channel": {channelID}, "limit": {"100"}, "inclusive": {"false"}}
	if oldest != "" {
		query.Set("oldest", oldest)
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var result struct {
		OK       bool               `json:"ok"`
		Error    string             `json:"error"`
		Messages []slackMessageWire `json:"messages"`
		Metadata struct {
			NextCursor string `json:"next_cursor"`
		} `json:"response_metadata"`
	}
	if err := c.get(ctx, "conversations.history", query, &result); err != nil {
		return SlackChatPage{}, err
	}
	if !result.OK {
		return SlackChatPage{}, errors.New("slack history unavailable: " + result.Error)
	}
	page := SlackChatPage{NextCursor: strings.TrimSpace(result.Metadata.NextCursor)}
	for _, message := range result.Messages {
		page.Messages = append(page.Messages, normalizeSlackMessage(message))
	}
	return page, nil
}

func (c *slackWebClient) Replies(ctx context.Context, channelID, timestamp string) ([]SlackChatMessage, error) {
	var result struct {
		OK       bool               `json:"ok"`
		Error    string             `json:"error"`
		Messages []slackMessageWire `json:"messages"`
	}
	if err := c.get(ctx, "conversations.replies", url.Values{"channel": {channelID}, "ts": {timestamp}, "limit": {"100"}}, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, errors.New("slack replies unavailable: " + result.Error)
	}
	items := make([]SlackChatMessage, 0, len(result.Messages))
	for _, message := range result.Messages {
		items = append(items, normalizeSlackMessage(message))
	}
	return items, nil
}

func (c *slackWebClient) Post(ctx context.Context, channelID, text, threadTS, idempotencyKey string) (string, error) {
	payload := map[string]any{"channel": channelID, "text": text, "unfurl_links": false,
		"unfurl_media": false, "metadata": map[string]any{"event_type": "misty_message",
			"event_payload": map[string]any{"message_id": idempotencyKey}}}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}
	raw, err := providerJSONRequest(ctx, c.token, c.tokenType, http.MethodPost,
		"https://slack.com/api/chat.postMessage", payload, nil)
	if err != nil {
		return "", err
	}
	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		TS    string `json:"ts"`
	}
	if json.Unmarshal(raw, &result) != nil || !result.OK || result.TS == "" {
		return "", errors.New("slack post failed: " + result.Error)
	}
	return result.TS, nil
}
