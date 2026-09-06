package apprpc

import (
	"encoding/json"
	"io"
)

const ordinaryEnvelopeLimit = 4 << 20
const mailJSONLimit = 28 << 20
const mailEnvelopeLimit = mailJSONLimit + (64 << 10)

func isMailProviderParameter(method, key string) bool {
	switch method {
	case "mail.threads.get", "mail.threads.action":
		return key == "threadID"
	case "mail.drafts.update", "mail.drafts.send":
		return key == "draftID"
	}
	return false
}

func canWriteMail(identity Identity) bool {
	for _, scope := range identity.Scopes {
		if scope == "mail.write" {
			return true
		}
	}
	return false
}

type countedReader struct {
	io.Reader
	bytes int64
}

func (reader *countedReader) Read(value []byte) (int, error) {
	n, err := reader.Reader.Read(value)
	reader.bytes += int64(n)
	return n, err
}

func validateMailEnvelope(request Request, size int64, identity Identity) error {
	draft := request.Method == "mail.drafts.create" || request.Method == "mail.drafts.update"
	if size > ordinaryEnvelopeLimit && (!draft || !canWriteMail(identity)) || draft && len(request.Params.Body) > mailJSONLimit {
		return &Error{"invalid_request", "Request body is too large."}
	}
	if request.Method == "mail.drafts.send" {
		var input struct {
			ConnectionID    string `json:"connection_id"`
			AuthoringSource string `json:"authoring_source"`
			Confirmed       bool   `json:"confirmed"`
		}
		if json.Unmarshal(request.Params.Body, &input) != nil || !identifier.MatchString(input.ConnectionID) ||
			!input.Confirmed || input.AuthoringSource != "user" && input.AuthoringSource != "ai" {
			return &Error{"invalid_params", "Mail send requires explicit user confirmation and authoring source."}
		}
	}
	return nil
}
