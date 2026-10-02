package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (s *AIService) awaitAIInvocationAnswer(r *http.Request, userID, invocationID string) (string, []aiCitation, error) {
	cursor := int64(0)
	stream, release := s.streams.acquire(userID, invocationID, cursor)
	defer release()
	answer := ""
	citations := []aiCitation{}
	for {
		page, err := stream.read(r.Context(), cursor)
		if err != nil {
			return "", nil, err
		}
		if page.ready {
			cursor = page.cursor
		}
		for _, item := range page.events {
			var event aiInvocationEvent
			if err := json.Unmarshal(item.payload, &event); err != nil {
				return "", nil, err
			}
			cursor = item.sequence
			if event.Citation != nil {
				citations = append(citations, *event.Citation)
			}
			if event.Type == "assistant.message" && strings.TrimSpace(event.Text) != "" {
				answer = event.Text
			}
			if event.Type == "invocation.failed" {
				return "", citations, fmt.Errorf("%s", firstAIText(event.Error, "Misty could not complete this request."))
			}
		}
		if page.ready && cursor >= page.head && aiInvocationTerminal(page.state) {
			if strings.TrimSpace(answer) == "" {
				return "", citations, fmt.Errorf("Misty returned no answer")
			}
			return answer, citations, nil
		}
		if len(page.events) > 0 {
			continue
		}
		select {
		case <-r.Context().Done():
			return "", citations, r.Context().Err()
		case <-page.notify:
		}
	}
}
