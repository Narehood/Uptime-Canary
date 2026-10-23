package jsonata

import "unicode/utf8"

// The retained stringToArray uses one element per rune. Count those codepoints
// without allocating the rune slice and one string per element.
func countStringCodepoints(value string) int {
	return utf8.RuneCountInString(value)
}
