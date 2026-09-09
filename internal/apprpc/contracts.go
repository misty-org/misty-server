// Package apprpc resolves versioned SDK method contracts without accepting URLs
// or HTTP verbs from downloaded Apps. Domain handlers remain authoritative.
package apprpc

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

//go:embed methods.json
var contractJSON []byte

type Method struct {
	Verb string `json:"verb"`
	Path string `json:"path"`
}
type Contract struct {
	Protocol int               `json:"protocol"`
	Methods  map[string]Method `json:"methods"`
}
type Request struct {
	Protocol int    `json:"protocol"`
	Method   string `json:"method"`
	Params   Params `json:"params"`
}
type Params struct {
	Path  map[string]string          `json:"path,omitempty"`
	Query map[string]json.RawMessage `json:"query,omitempty"`
	Body  json.RawMessage            `json:"body,omitempty"`
}
type Target struct {
	Verb  string
	Path  string
	Query string
	Body  []byte
}
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

var placeholder = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9]*)\}`)
var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
var mailProviderIdentifier = regexp.MustCompile(`^[\x21-\x7e]{1,320}$`)
var capabilityProviderIdentifier = regexp.MustCompile(`^[a-z][a-z0-9.-]+/[a-z][a-z0-9_-]*$`)
var contract = loadContract()

func loadContract() Contract {
	var value Contract
	if err := json.Unmarshal(contractJSON, &value); err != nil {
		panic(err)
	}
	return value
}

func Decode(reader io.Reader) (Request, error) {
	var request Request
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, &Error{"invalid_request", "Invalid SDK method envelope."}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return request, &Error{"invalid_request", "Only one SDK method is allowed per request."}
	}
	return request, nil
}

func Resolve(request Request, spaceID string) (Target, error) {
	if request.Protocol != contract.Protocol {
		return Target{}, &Error{"unsupported_protocol", "This SDK protocol is not supported."}
	}
	method, exists := contract.Methods[request.Method]
	if !exists {
		return Target{}, &Error{"unsupported_method", "This SDK method is not supported."}
	}
	if strings.Contains(method.Path, "{spaceID}") && !identifier.MatchString(spaceID) {
		return Target{}, &Error{"invalid_params", "A bound Space is required."}
	}
	if supplied := request.Params.Path["spaceID"]; supplied != "" && supplied != spaceID {
		return Target{}, &Error{"space_mismatch", "The method belongs to another Space."}
	}
	path := method.Path
	used := map[string]bool{"spaceID": true}
	for _, match := range placeholder.FindAllStringSubmatch(path, -1) {
		key := match[1]
		value := request.Params.Path[key]
		used[key] = true
		if key == "spaceID" {
			if value != "" && value != spaceID {
				return Target{}, &Error{"space_mismatch", "The method belongs to another Space."}
			}
			value = spaceID
		}
		valid := identifier.MatchString(value)
		if strings.HasPrefix(request.Method, "capabilities.") && key == "providerID" {
			valid = len(value) <= 240 && capabilityProviderIdentifier.MatchString(value)
		}
		if isMailProviderParameter(request.Method, key) {
			valid = mailProviderIdentifier.MatchString(value) && value != "." && value != ".."
		}
		if !valid {
			return Target{}, &Error{"invalid_params", fmt.Sprintf("Invalid %s parameter.", key)}
		}
		path = strings.ReplaceAll(path, match[0], url.PathEscape(value))
	}
	for key := range request.Params.Path {
		if !used[key] {
			return Target{}, &Error{"invalid_params", "Unknown method path parameter."}
		}
	}
	query := url.Values{}
	if len(request.Params.Query) > 64 {
		return Target{}, &Error{"invalid_params", "Too many query parameters."}
	}
	for key, raw := range request.Params.Query {
		if !identifier.MatchString(key) {
			return Target{}, &Error{"invalid_params", "Invalid query parameter name."}
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return Target{}, &Error{"invalid_params", "Invalid query value."}
		}
		values := []any{value}
		if array, ok := value.([]any); ok {
			values = array
		}
		if len(values) > 100 {
			return Target{}, &Error{"invalid_params", "Too many query values."}
		}
		for _, item := range values {
			var text string
			switch typed := item.(type) {
			case string:
				text = typed
			case json.Number:
				text = typed.String()
			case bool:
				text = strconv.FormatBool(typed)
			default:
				return Target{}, &Error{"invalid_params", "Query values must be strings, numbers, or booleans."}
			}
			if len(text) > 8192 || strings.ContainsRune(text, 0) {
				return Target{}, &Error{"invalid_params", "Invalid query value."}
			}
			query.Add(key, text)
		}
	}
	body := []byte(request.Params.Body)
	if len(body) != 0 && !json.Valid(body) {
		return Target{}, &Error{"invalid_params", "Invalid method body."}
	}
	if (method.Verb == "GET" || method.Verb == "HEAD") && len(body) > 0 && string(body) != "null" {
		return Target{}, &Error{"invalid_params", "This method does not accept a body."}
	}
	return Target{Verb: method.Verb, Path: path, Query: query.Encode(), Body: body}, nil
}

func Methods() map[string]Method {
	output := make(map[string]Method, len(contract.Methods))
	for name, method := range contract.Methods {
		output[name] = method
	}
	return output
}
