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

// NormalizePhone cleans phone numbers to a standard digits-only format with 998 prefix.
// e.g. "+998 (90) 123-45-67" -> "998901234567", "90 123-45-67" -> "998901234567"
func NormalizePhone(phone string) string {
	// Remove all non-digits
	reg := regexp.MustCompile(`\D`)
	cleaned := reg.ReplaceAllString(phone, "")

	// If it is 9 digits, prepend 998 country code
	if len(cleaned) == 9 {
		cleaned = "998" + cleaned
	}
	return cleaned
}
