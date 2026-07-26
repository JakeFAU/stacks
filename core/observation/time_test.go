package observation_test

import (
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/observation"
)

func TestUnknownTimeHasNoBoundsOrInstant(t *testing.T) {
	extent := observation.UnknownTime()
	if extent.Kind() != observation.TemporalUnknown {
		t.Errorf("Kind() = %v, want %v", extent.Kind(), observation.TemporalUnknown)
	}
	if _, ok := extent.Instant(); ok {
		t.Fatal("Instant() ok = true for unknown time")
	}
	if _, hasStart, _, hasEnd := extent.Bounds(); hasStart || hasEnd {
		t.Fatalf("Bounds() = (_, %v, _, %v), want no bounds", hasStart, hasEnd)
	}
}

func TestAtTimeNormalizesToUTC(t *testing.T) {
	location := time.FixedZone("synthetic", -5*60*60)
	input := time.Date(2026, time.January, 4, 9, 30, 0, 0, location)
	extent, err := observation.AtTime(input)
	if err != nil {
		t.Fatalf("AtTime() error = %v", err)
	}

	instant, ok := extent.Instant()
	if !ok || !instant.Equal(input) || instant.Location() != time.UTC {
		t.Errorf("Instant() = (%v, %v), want UTC equivalent of %v", instant, ok, input)
	}
}

func TestDuringUsesHalfOpenIncreasingUTCBounds(t *testing.T) {
	location := time.FixedZone("synthetic", 2*60*60)
	start := time.Date(2026, time.February, 1, 9, 0, 0, 0, location)
	end := start.Add(time.Hour)
	extent, err := observation.During(start, end)
	if err != nil {
		t.Fatalf("During() error = %v", err)
	}

	gotStart, hasStart, gotEnd, hasEnd := extent.Bounds()
	if extent.Kind() != observation.TemporalInterval || !hasStart || !hasEnd {
		t.Fatalf("During() kind/bounds = (%v, %v, %v), want interval with both bounds", extent.Kind(), hasStart, hasEnd)
	}
	if !gotStart.Equal(start) || !gotEnd.Equal(end) || gotStart.Location() != time.UTC || gotEnd.Location() != time.UTC {
		t.Errorf("Bounds() = (%v, %v), want UTC equivalents of (%v, %v)", gotStart, gotEnd, start, end)
	}
}

func TestDuringRejectsMissingOrNonIncreasingBounds(t *testing.T) {
	instant := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name  string
		start time.Time
		end   time.Time
	}{
		{name: "missing start", end: instant},
		{name: "missing end", start: instant},
		{name: "equal", start: instant, end: instant},
		{name: "decreasing", start: instant, end: instant.Add(-time.Second)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := observation.During(test.start, test.end); err == nil {
				t.Fatal("During() error = nil, want invalid interval error")
			}
		})
	}
}

func TestOpenIntervalsDistinguishUnknownBounds(t *testing.T) {
	start := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	since, err := observation.Since(start)
	if err != nil {
		t.Fatalf("Since() error = %v", err)
	}
	gotStart, hasStart, gotEnd, hasEnd := since.Bounds()
	if !hasStart || gotStart != start || hasEnd || !gotEnd.IsZero() {
		t.Errorf("Since().Bounds() = (%v, %v, %v, %v), want (%v, true, zero, false)", gotStart, hasStart, gotEnd, hasEnd, start)
	}

	end := start.Add(time.Hour)
	until, err := observation.Until(end)
	if err != nil {
		t.Fatalf("Until() error = %v", err)
	}
	gotStart, hasStart, gotEnd, hasEnd = until.Bounds()
	if hasStart || !gotStart.IsZero() || !hasEnd || gotEnd != end {
		t.Errorf("Until().Bounds() = (%v, %v, %v, %v), want (zero, false, %v, true)", gotStart, hasStart, gotEnd, hasEnd, end)
	}
}

func TestUntilNormalizesToUTCMicrosecondPrecision(t *testing.T) {
	input := time.Date(2026, time.January, 4, 9, 30, 0, 123456789, time.FixedZone("synthetic", -5*60*60))
	want := time.Date(2026, time.January, 4, 14, 30, 0, 123456000, time.UTC)

	extent, err := observation.Until(input)
	if err != nil {
		t.Fatal(err)
	}
	_, hasStart, got, hasEnd := extent.Bounds()
	if hasStart || !hasEnd || got != want {
		t.Fatalf("Until().Bounds() = (_, %v, %v, %v), want (_, false, %v, true)", hasStart, got, hasEnd, want)
	}
}

func TestWithinRepresentsUncertainInstantAndRejectsZeroDuration(t *testing.T) {
	start := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(1, 0, 0)
	extent, err := observation.Within(start, end)
	if err != nil {
		t.Fatalf("Within() error = %v", err)
	}
	if extent.Kind() != observation.TemporalWindow {
		t.Errorf("Kind() = %v, want %v", extent.Kind(), observation.TemporalWindow)
	}
	if _, err := observation.Within(start, start); err == nil {
		t.Fatal("Within() error = nil, want zero-duration window error")
	}
}

func TestTemporalConstructorsRejectZeroTimes(t *testing.T) {
	if _, err := observation.AtTime(time.Time{}); err == nil {
		t.Fatal("AtTime() error = nil, want missing instant error")
	}
	if _, err := observation.Since(time.Time{}); err == nil {
		t.Fatal("Since() error = nil, want missing start error")
	}
	if _, err := observation.Until(time.Time{}); err == nil {
		t.Fatal("Until() error = nil, want missing end error")
	}
	end := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	if _, err := observation.Within(time.Time{}, end); err == nil {
		t.Fatal("Within() error = nil, want missing start error")
	}
}
