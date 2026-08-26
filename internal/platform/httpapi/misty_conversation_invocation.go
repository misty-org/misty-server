package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *AIService) awaitAIInvocationAnswer(r *http.Request, userID, invocationID string) (string, []aiCitation, error) {
	cursor := 0
	answer := ""
	citations := []aiCitation{}
	for {
		events, state, notify, found := s.invocations.events(userID, invocationID, cursor)
		if !found {
			return "", nil, db.ErrSpaceNotFound
		}
		for _, event := range events {
			if value, err := strconv.Atoi(event.ID); err == nil {
				cursor = value
			}
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
		if aiInvocationTerminal(state) {
			if strings.TrimSpace(answer) == "" {
				return "", citations, fmt.Errorf("Misty returned no answer")
			}
			return answer, citations, nil
		}
		select {
		case <-r.Context().Done():
			return "", citations, r.Context().Err()
		case <-notify:
		}
	}
}
