// Package strutil provides small string helpers.
package strutil

import "math/rand/v2"

// Random returns n ASCII letters and digits. It is not cryptographically secure.
// A negative length panics. It is safe for concurrent use.
func Random(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rand.IntN(len(alphabet))]
	}
	return string(b)
}

// Lines splits LF, CRLF and CR lines, preserving internal empty lines.
// Empty input yields an empty slice; a final separator adds no extra line.
func Lines(s string) []string {
	lines := make([]string, 0)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\r' && s[i] != '\n' {
			continue
		}
		lines = append(lines, s[start:i])
		if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
			i++
		}
		start = i + 1
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
