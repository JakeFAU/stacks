package observation_test

import (
	"math"
	"strconv"
	"testing"

	"github.com/JakeFAU/stacks/core/observation"
)

func TestUnitIntervalConfidenceAcceptsClosedUnitInterval(t *testing.T) {
	for _, value := range []float64{0, 0.75, 1} {
		t.Run(confidenceCaseName(value), func(t *testing.T) {
			confidence, err := observation.NewUnitIntervalConfidence(value)
			if err != nil {
				t.Fatalf("NewUnitIntervalConfidence(%v) error = %v", value, err)
			}
			if confidence.Value() != value {
				t.Errorf("Value() = %v, want %v", confidence.Value(), value)
			}
			if confidence.Scale() != observation.ConfidenceUnitInterval {
				t.Errorf("Scale() = %q, want %q", confidence.Scale(), observation.ConfidenceUnitInterval)
			}
		})
	}
}

func TestUnitIntervalConfidenceRejectsOutsideRangeAndNonFinite(t *testing.T) {
	for _, value := range []float64{-0.01, 1.01, math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run(confidenceCaseName(value), func(t *testing.T) {
			if _, err := observation.NewUnitIntervalConfidence(value); err == nil {
				t.Fatalf("NewUnitIntervalConfidence(%v) error = nil, want validation error", value)
			}
		})
	}
}

func TestLegacyConfidencePreservesEveryFiniteValue(t *testing.T) {
	for _, value := range []float64{-2.5, 0.75, 4.25} {
		t.Run(confidenceCaseName(value), func(t *testing.T) {
			confidence, err := observation.NewLegacyConfidence(value)
			if err != nil {
				t.Fatalf("NewLegacyConfidence(%v) error = %v", value, err)
			}
			if confidence.Value() != value {
				t.Errorf("Value() = %v, want %v", confidence.Value(), value)
			}
			if confidence.Scale() != observation.ConfidenceUnspecifiedLegacy {
				t.Errorf("Scale() = %q, want %q", confidence.Scale(), observation.ConfidenceUnspecifiedLegacy)
			}
		})
	}
}

func TestLegacyConfidenceRejectsNonFinite(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run(confidenceCaseName(value), func(t *testing.T) {
			if _, err := observation.NewLegacyConfidence(value); err == nil {
				t.Fatalf("NewLegacyConfidence(%v) error = nil, want validation error", value)
			}
		})
	}
}

func confidenceCaseName(value float64) string {
	switch {
	case math.IsNaN(value):
		return "nan"
	case math.IsInf(value, 1):
		return "positive-infinity"
	case math.IsInf(value, -1):
		return "negative-infinity"
	default:
		return strconv.FormatFloat(value, 'g', -1, 64)
	}
}
