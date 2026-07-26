package observation

import (
	"math"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/internal/canonicalhash"
)

// ObservationDigestVersion identifies the canonical semantic observation
// encoding. Observation retry IDs are intentionally not part of this digest.
const ObservationDigestVersion = "stacks.observation.v2.canonical"

func digestObservation(value Observation) evidence.ContentDigest {
	encoder := canonicalhash.New(ObservationDigestVersion)
	writeTerm(encoder, value.statement.Subject)
	encoder.String(string(value.statement.Predicate))
	writeTerm(encoder, value.statement.Object)
	writeTemporalExtent(encoder, value.validTime)
	encoder.Time(value.recordedAt)
	encoder.Uint64(uint64(len(value.evidence)))
	for _, link := range value.evidence {
		encoder.String(string(link.EvidenceID))
		encoder.String(string(link.Role))
	}
	encoder.String(value.derivation.Method)
	encoder.String(value.derivation.Version)
	encoder.String(value.derivation.RunID)
	encoder.String(value.derivation.Model)
	encoder.String(value.derivation.PromptVersion)
	// Preserve the canonical v2 field positions after retiring unsupported
	// compatibility states.
	encoder.Bool(false)
	encoder.String(string(value.status))
	encoder.Bool(value.hasConfidence)
	if value.hasConfidence {
		encoder.Uint64(math.Float64bits(value.confidence.value))
		encoder.String(string(value.confidence.scale))
	}
	encoder.Bool(false)
	return evidence.ContentDigest(encoder.Sum())
}

func writeTerm(encoder *canonicalhash.Encoder, term Term) {
	encoder.Uint64(uint64(term.kind))
	encoder.String(term.text)
	encoder.String(term.mentionID)
	encoder.String(term.entityID)
	encoder.String(term.groundingMentionID)
}

func writeTemporalExtent(encoder *canonicalhash.Encoder, extent TemporalExtent) {
	encoder.Uint64(uint64(extent.kind))
	encoder.Bool(extent.hasStart)
	if extent.hasStart {
		encoder.Time(extent.start)
	}
	encoder.Bool(extent.hasEnd)
	if extent.hasEnd {
		encoder.Time(extent.end)
	}
}
