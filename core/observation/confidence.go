package observation

import (
	"fmt"
	"math"
)

// ConfidenceScale declares how a model-provided score may be interpreted.
type ConfidenceScale string

const (
	ConfidenceUnitInterval      ConfidenceScale = "unit_interval"
	ConfidenceUnspecifiedLegacy ConfidenceScale = "unspecified_legacy"
)

// Confidence is immutable model metadata. It does not establish truth or
// promote epistemic status.
type Confidence struct {
	value float64
	scale ConfidenceScale
}

// NewUnitIntervalConfidence creates a normalized score in the closed interval
// [0, 1].
func NewUnitIntervalConfidence(value float64) (Confidence, error) {
	if !finite(value) || value < 0 || value > 1 {
		return Confidence{}, fmt.Errorf("unit-interval confidence must be finite and between 0 and 1")
	}
	return Confidence{value: value, scale: ConfidenceUnitInterval}, nil
}

// NewLegacyConfidence preserves a finite historical score whose scale was not
// declared.
func NewLegacyConfidence(value float64) (Confidence, error) {
	if !finite(value) {
		return Confidence{}, fmt.Errorf("legacy confidence must be finite")
	}
	return Confidence{value: value, scale: ConfidenceUnspecifiedLegacy}, nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// Value returns the supplied score.
func (confidence Confidence) Value() float64 {
	return confidence.value
}

// Scale returns the declared interpretation of the score.
func (confidence Confidence) Scale() ConfidenceScale {
	return confidence.scale
}
