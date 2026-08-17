package utils

import (
	"regexp"
	"strings"
)

var classNameHyphenRegex = regexp.MustCompile(`\s*-\s*`)
var multipleSpaceRegex = regexp.MustCompile(`\s+`)

// NormalizeClassName normalizes class names for robust matching and clean storage.
// e.g. "4- A" -> "4-A", " 4 - a " -> "4-A", "10  -  b" -> "10-B"
func NormalizeClassName(name string) string {
	if name == "" {
		return ""
	}
	s := strings.TrimSpace(strings.ToUpper(name))
	s = classNameHyphenRegex.ReplaceAllString(s, "-")
	s = multipleSpaceRegex.ReplaceAllString(s, "")
	return s
}
