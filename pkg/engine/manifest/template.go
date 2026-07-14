package manifest

import (
	"strings"
)

// Substitute replaces {token} placeholders in s with vars[token].
// Unknown tokens are left as-is so callers can detect missing values.
//
// Used by provider for ready_check.url ({addr}) and by isannd's container
// template resolver (host port / volume paths / env values).
func Substitute(s string, vars map[string]string) string {
	return substitute(s, vars)
}

func substitute(s string, vars map[string]string) string {
	if !strings.ContainsRune(s, '{') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '{' {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			b.WriteString(s[i:])
			break
		}
		key := s[i+1 : i+end]
		if v, ok := vars[key]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(s[i : i+end+1])
		}
		i += end + 1
	}
	return b.String()
}
