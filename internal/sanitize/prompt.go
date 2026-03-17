package sanitize

import "strings"

// ForPrompt strips characters that could escape a prompt section boundary.
// Newlines and carriage returns are replaced with spaces to prevent injecting
// fake section headers (e.g. "[Retrieved Context]\nhijacked chunk") via
// user-controlled profile fields.
func ForPrompt(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}
