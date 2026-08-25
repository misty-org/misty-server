package api

import (
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestingSpansToPlainText(spans []db.MessageSpan) string {
	var builder strings.Builder
	for _, span := range spans {
		switch span.Type {
		case "text", "":
			builder.WriteString(span.Text)
		case "mention":
			if span.Label != "" {
				builder.WriteString("@" + span.Label)
			} else if span.Text != "" {
				builder.WriteString(span.Text)
			}
		case "link":
			if span.Text != "" {
				builder.WriteString(span.Text)
			} else if span.Label != "" {
				builder.WriteString(span.Label)
			} else if span.URL != "" {
				builder.WriteString(span.URL)
			}
		default:
			if span.Text != "" {
				builder.WriteString(span.Text)
			}
		}
	}
	return builder.String()
}
