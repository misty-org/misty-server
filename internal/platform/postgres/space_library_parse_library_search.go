package db

import (
	"strconv"
	"strings"
	"time"
)

func TestingParseLibrarySearch(input string) (parsedLibrarySearch, error) {
	parsed := parsedLibrarySearch{}
	textTokens := []string{}
	for _, token := range splitLibrarySearchTokens(input) {
		key, value, structured := strings.Cut(token, ":")
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		if !structured || value == "" {
			textTokens = append(textTokens, token)
			continue
		}
		switch key {
		case "tag":
			if len([]rune(value)) > 80 {
				return parsed, ErrLibraryInvalid
			}
			parsed.Tags = append(parsed.Tags, value)
		case "type":
			switch strings.ToLower(value) {
			case "image", "images", "photo", "photos":
				parsed.MediaType = "image"
			case "video", "videos":
				parsed.MediaType = "video"
			case "audio":
				parsed.MediaType = "audio"
			case "document", "documents", "file", "files":
				parsed.MediaType = "document"
			case "selfie", "selfies":
				parsed.MediaType = "selfies"
			case "live-photo", "live-photos":
				parsed.MediaType = "live-photos"
			case "portrait", "portraits":
				parsed.MediaType = "portraits"
			case "panorama", "panoramas", "pano":
				parsed.MediaType = "panoramas"
			case "slo-mo", "slow-motion":
				parsed.MediaType = "slo-mo"
			case "cinematic":
				parsed.MediaType = "cinematic"
			case "burst", "bursts":
				parsed.MediaType = "bursts"
			case "screenshot", "screenshots":
				parsed.MediaType = "screenshots"
			case "screen-recording", "screen-recordings":
				parsed.MediaType = "screen-recordings"
			case "spatial":
				parsed.MediaType = "spatial"
			default:
				return parsed, ErrLibraryInvalid
			}
		case "album":
			if len([]rune(value)) > 120 {
				return parsed, ErrLibraryInvalid
			}
			parsed.Album = value
		case "favorite", "hidden":
			boolean, parseErr := strconv.ParseBool(strings.ToLower(value))
			if parseErr != nil {
				return parsed, ErrLibraryInvalid
			}
			if key == "favorite" {
				parsed.Favorite = &boolean
			} else {
				parsed.Hidden = &boolean
			}
		case "after", "before":
			date, parseErr := time.Parse("2006-01-02", value)
			if parseErr != nil {
				return parsed, ErrLibraryInvalid
			}
			if key == "after" {
				parsed.DateFrom = &date
			} else {
				parsed.DateTo = &date
			}
		case "year":
			year, parseErr := strconv.Atoi(value)
			if parseErr != nil || year < 1 || year > 9999 {
				return parsed, ErrLibraryInvalid
			}
			from := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
			to := from.AddDate(1, 0, 0)
			parsed.DateFrom, parsed.DateTo = &from, &to
		default:
			textTokens = append(textTokens, token)
		}
	}
	parsed.Text = strings.Join(textTokens, " ")
	return parsed, nil
}

func splitLibrarySearchTokens(input string) []string {
	tokens := []string{}
	var current strings.Builder
	quoted := false
	for _, char := range input {
		switch {
		case char == '"':
			quoted = !quoted
		case !quoted && (char == ' ' || char == '\t' || char == '\n'):
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(char)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}
