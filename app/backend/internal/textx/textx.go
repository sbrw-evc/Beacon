// Package textx cuts user-visible text without splitting UTF-8 characters.
package textx

import "unicode/utf8"

// Runes returns s cut to at most n characters (runes). Limits on text people
// read — titles, comments, messages — are counted in characters so a Cyrillic
// comment gets the same room as a Latin one.
func Runes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// Bytes returns s cut to at most n bytes, backing off to the start of a
// character so the result stays valid UTF-8. Use it where the limit protects
// storage or a wire format rather than a reader.
func Bytes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
