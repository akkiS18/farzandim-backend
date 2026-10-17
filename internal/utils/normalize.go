package utils

import (
	"fmt"
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

// FixRomanPrefix fixes optical recognition quirks in Roman numeral prefixes (e.g. '1', 'L', '|' -> 'I')
func FixRomanPrefix(prefix string) string {
	upper := strings.ToUpper(strings.TrimSpace(prefix))
	upper = strings.ReplaceAll(upper, "L", "I")
	upper = strings.ReplaceAll(upper, "1", "I")
	upper = strings.ReplaceAll(upper, "|", "I")
	return upper
}

var (
	reBirth1 = regexp.MustCompile(`(?i)^([ivxl1\|]+)[\s\.-]+([a-z]{2})[\s\.-]*(\d+)$`)
	reBirth2 = regexp.MustCompile(`(?i)^([ivxl1\|]{1,4})([a-z]{2})[\s\.-]*(\d+)$`)
	rePass   = regexp.MustCompile(`(?i)^([a-z]{2})[\s\.-]*(\d{6,8})$`)
)

// NormalizeDocumentNo formats passport and birth certificate numbers into standardized representation
func NormalizeDocumentNo(doc string) string {
	clean := strings.TrimSpace(doc)
	if clean == "" || clean == "-" || strings.EqualFold(clean, "yo'q") {
		return clean
	}

	// 1. Birth certificate with separator (e.g. I-NA. 086354, l-NA 086354, l-na.086354, 1-NA 086354, II-NA 086354, etc.)
	if matches := reBirth1.FindStringSubmatch(clean); len(matches) == 4 {
		roman := FixRomanPrefix(matches[1])
		series := strings.ToUpper(matches[2])
		num := matches[3]
		return fmt.Sprintf("%s-%s %s", roman, series, num)
	}

	// 2. Birth certificate without separator (e.g. ina086354, lna086354, iina 086354)
	if matches := reBirth2.FindStringSubmatch(clean); len(matches) == 4 {
		roman := FixRomanPrefix(matches[1])
		series := strings.ToUpper(matches[2])
		num := matches[3]
		return fmt.Sprintf("%s-%s %s", roman, series, num)
	}

	// 3. Passport: 2 letters + numbers (e.g. AB 1234567 or AB. 1234567 -> AB1234567)
	if matches := rePass.FindStringSubmatch(clean); len(matches) == 3 {
		return fmt.Sprintf("%s%s", strings.ToUpper(matches[1]), matches[2])
	}

	return clean
}

// SplitFIO parses a full name into last name, first name, and middle name
func SplitFIO(fullName string) (lastName, firstName, middleName string) {
	clean := strings.TrimSpace(fullName)
	parts := strings.Fields(clean)
	if len(parts) == 0 {
		return "", "", ""
	}
	lastName = parts[0]
	if len(parts) > 1 {
		firstName = parts[1]
	}
	if len(parts) > 2 {
		middleName = strings.Join(parts[2:], " ")
	}
	return lastName, firstName, middleName
}

