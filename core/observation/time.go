package observation

import (
	"fmt"
	"time"
)

// TemporalKind distinguishes the temporal meanings an observation may carry.
type TemporalKind uint8

const (
	TemporalUnknown TemporalKind = iota
	TemporalInstant
	TemporalInterval
	TemporalWindow
)

// TemporalExtent describes when an observation was valid in the source world.
// Bounded intervals and uncertainty windows are half-open.
type TemporalExtent struct {
	kind     TemporalKind
	start    time.Time
	end      time.Time
	hasStart bool
	hasEnd   bool
}

// UnknownTime represents source time that is genuinely unknown.
func UnknownTime() TemporalExtent {
	return TemporalExtent{kind: TemporalUnknown}
}

// AtTime represents an observation valid at a specific instant.
func AtTime(instant time.Time) (TemporalExtent, error) {
	if instant.IsZero() {
		return TemporalExtent{}, fmt.Errorf("valid instant is required")
	}
	return TemporalExtent{
		kind:     TemporalInstant,
		start:    instant.UTC(),
		hasStart: true,
	}, nil
}

// During represents an observation valid during the half-open interval
// [start, end).
func During(start, end time.Time) (TemporalExtent, error) {
	if start.IsZero() || end.IsZero() {
		return TemporalExtent{}, fmt.Errorf("valid interval bounds are required")
	}
	if !end.After(start) {
		return TemporalExtent{}, fmt.Errorf("valid interval end must be after start")
	}
	return TemporalExtent{
		kind:     TemporalInterval,
		start:    start.UTC(),
		end:      end.UTC(),
		hasStart: true,
		hasEnd:   true,
	}, nil
}

// Since represents an observation valid from start with no known end.
func Since(start time.Time) (TemporalExtent, error) {
	if start.IsZero() {
		return TemporalExtent{}, fmt.Errorf("valid interval start is required")
	}
	return TemporalExtent{
		kind:     TemporalInterval,
		start:    start.UTC(),
		hasStart: true,
	}, nil
}

// Until represents an observation valid before end with no known start.
func Until(end time.Time) (TemporalExtent, error) {
	if end.IsZero() {
		return TemporalExtent{}, fmt.Errorf("valid interval end is required")
	}
	return TemporalExtent{
		kind:   TemporalInterval,
		end:    end.UTC(),
		hasEnd: true,
	}, nil
}

// Within represents an instant known only to have occurred somewhere in the
// half-open uncertainty window [start, end).
func Within(start, end time.Time) (TemporalExtent, error) {
	if start.IsZero() || end.IsZero() {
		return TemporalExtent{}, fmt.Errorf("valid time window bounds are required")
	}
	if !end.After(start) {
		return TemporalExtent{}, fmt.Errorf("valid time window end must be after start")
	}
	return TemporalExtent{
		kind:     TemporalWindow,
		start:    start.UTC(),
		end:      end.UTC(),
		hasStart: true,
		hasEnd:   true,
	}, nil
}

// Kind returns the temporal extent kind.
func (extent TemporalExtent) Kind() TemporalKind {
	return extent.kind
}

// Instant returns the valid instant when Kind is TemporalInstant.
func (extent TemporalExtent) Instant() (time.Time, bool) {
	return extent.start, extent.kind == TemporalInstant
}

// Bounds returns interval or uncertainty-window bounds. hasStart and hasEnd
// distinguish open bounds from actual timestamps.
func (extent TemporalExtent) Bounds() (start time.Time, hasStart bool, end time.Time, hasEnd bool) {
	if extent.kind != TemporalInterval && extent.kind != TemporalWindow {
		return time.Time{}, false, time.Time{}, false
	}
	return extent.start, extent.hasStart, extent.end, extent.hasEnd
}
