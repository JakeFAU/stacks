// Package timepoint defines the canonical representation for durable times.
package timepoint

import "time"

// Precision is the finest durable time precision supported by Stacks.
const Precision = time.Microsecond

// Normalize returns zero unchanged and every other value in UTC microsecond
// precision without a monotonic clock reading.
func Normalize(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.Round(0).UTC().Truncate(Precision)
}

// IsCanonical reports whether value already has the durable representation.
func IsCanonical(value time.Time) bool {
	return value.Location() == time.UTC && value == Normalize(value)
}
