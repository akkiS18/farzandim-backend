package utils

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func ValidateAdminPassword(password string) error {
	if utf8.RuneCountInString(password) < 12 || len(password) > 72 || strings.TrimSpace(password) == "" {
		return fmt.Errorf("administrator paroli kamida 12 ta belgi va ko‘pi bilan 72 bayt bo‘lishi kerak")
	}
	return nil
}
