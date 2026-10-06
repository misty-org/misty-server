package api

import (
	"strings"
	"unicode"
)

// Member names are matched as whole words after normalizing punctuation.
func normalizeGroundingText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("’", "'", "‘", "'", "“", "\"", "”", "\"", "—", "-", "–", "-").Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func containsGroundingPhrase(value, phrase string) bool {
	words := func(input string) string {
		input = normalizeGroundingText(input)
		input = strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return ' '
		}, input)
		return strings.Join(strings.Fields(input), " ")
	}
	value, phrase = words(value), words(phrase)
	return phrase != "" && strings.Contains(" "+value+" ", " "+phrase+" ")
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
