// Package reasoncode validates bounded, non-secret authority reason codes.
package reasoncode

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaximumRunes is the largest durable authority reason code.
const MaximumRunes = 128

// Validate returns value unchanged when it is nonblank and within the durable
// Unicode rune boundary.
func Validate(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("reason code is required")
	}
	if utf8.RuneCountInString(value) > MaximumRunes {
		return "", fmt.Errorf("reason code must not exceed %d Unicode runes", MaximumRunes)
	}
	return value, nil
}
