// Package codegen contains naming helpers shared by generated Go code.
package codegen

import (
	"strings"
	"unicode"
)

var initialisms = map[string]string{
	"acu": "ACU", "api": "API", "http": "HTTP", "https": "HTTPS",
	"id": "ID", "ip": "IP", "json": "JSON", "pr": "PR", "rbac": "RBAC",
	"rpc": "RPC", "ssh": "SSH", "tls": "TLS", "url": "URL", "uuid": "UUID",
	"uri": "URI", "xml": "XML",
	"ids": "IDs", "urls": "URLs", "uris": "URIs", "uuids": "UUIDs",
	"apis": "APIs", "prs": "PRs", "idp": "IDP",
}

// GoName converts a schema or property name to an exported Go identifier,
// preserving common initialisms such as ID, URL, API, UUID, and HTTP.
func GoName(value string) string {
	words := splitWords(value)
	if len(words) == 0 {
		return "Field"
	}
	var out strings.Builder
	for _, word := range words {
		lower := strings.ToLower(word)
		if initialism, ok := initialisms[lower]; ok {
			out.WriteString(initialism)
			continue
		}
		runes := []rune(lower)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
		}
		out.WriteString(string(runes))
	}
	result := out.String()
	if result == "" || unicode.IsDigit([]rune(result)[0]) {
		return "Field" + result
	}
	return result
}

func splitWords(value string) []string {
	runes := []rune(value)
	words := make([]string, 0, len(runes)/2)
	start := -1
	flush := func(end int) {
		if start >= 0 && end > start {
			words = append(words, string(runes[start:end]))
		}
		start = -1
	}
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush(i)
			continue
		}
		if start < 0 {
			start = i
			continue
		}
		previous := runes[i-1]
		var next rune
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		boundary := (unicode.IsLower(previous) || unicode.IsDigit(previous)) && unicode.IsUpper(r) ||
			unicode.IsUpper(previous) && unicode.IsUpper(r) && next != 0 && unicode.IsLower(next) ||
			unicode.IsDigit(previous) != unicode.IsDigit(r)
		if boundary {
			flush(i)
			start = i
		}
	}
	flush(len(runes))
	return words
}
