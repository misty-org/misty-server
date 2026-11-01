package main

import (
	"encoding/json"
	"strings"
)

// decodeJSONC reads wrangler.jsonc: JSON with // and /* */ comments and
// trailing commas.
func decodeJSONC(data []byte, out any) error {
	return json.Unmarshal([]byte(dropTrailingCommas(stripComments(string(data)))), out)
}

// scanJSON calls visit for every byte outside string literals and copies
// string literals through unchanged. visit returns how many bytes it consumed
// (0 copies the byte as is).
func scanJSON(source string, visit func(rest string, b *strings.Builder) int) string {
	var b strings.Builder
	for i := 0; i < len(source); i++ {
		if source[i] == '"' {
			j := i + 1
			for j < len(source) && source[j] != '"' {
				if source[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(source) {
				j = len(source) - 1
			}
			b.WriteString(source[i : j+1])
			i = j
			continue
		}
		if consumed := visit(source[i:], &b); consumed > 0 {
			i += consumed - 1
			continue
		}
		b.WriteByte(source[i])
	}
	return b.String()
}

func stripComments(source string) string {
	return scanJSON(source, func(rest string, b *strings.Builder) int {
		switch {
		case strings.HasPrefix(rest, "//"):
			end := strings.IndexByte(rest, '\n')
			if end < 0 {
				return len(rest)
			}
			return end
		case strings.HasPrefix(rest, "/*"):
			end := strings.Index(rest[2:], "*/")
			if end < 0 {
				return len(rest)
			}
			b.WriteByte(' ')
			return end + 4
		}
		return 0
	})
}

func dropTrailingCommas(source string) string {
	return scanJSON(source, func(rest string, _ *strings.Builder) int {
		if rest[0] != ',' {
			return 0
		}
		next := strings.TrimLeft(rest[1:], " \t\r\n")
		if strings.HasPrefix(next, "}") || strings.HasPrefix(next, "]") {
			return 1
		}
		return 0
	})
}
