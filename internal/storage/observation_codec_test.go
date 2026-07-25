package storage

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
)

func TestObservationCompatibilityErrorSupportsErrorsIs(t *testing.T) {
	for _, want := range []error{
		ErrObservationNotRepresentable,
		ErrObservationCompatibility,
		ErrObservationConflict,
	} {
		err := fmt.Errorf("complete observation: %w", want)
		if !errors.Is(err, want) {
			t.Fatalf("errors.Is(%v, %v) = false", err, want)
		}
	}
}

func TestEncodeLegacyObservationRejectsUnrepresentableRecordedAt(t *testing.T) {
	const privatePredicate = "private_predicate_must_not_leak"
	notMicrosecond := time.Date(2026, time.July, 25, 12, 0, 0, 1, time.UTC)
	value := newCodecObservation(t, privatePredicate, observation.UnknownTime(), notMicrosecond)

	_, err := encodeLegacyObservation(value, legacyObservationCompatibility{}, nil, nil)
	if !errors.Is(err, ErrObservationNotRepresentable) ||
		!strings.Contains(err.Error(), "recorded_at_not_representable") ||
		strings.Contains(err.Error(), privatePredicate) {
		t.Fatalf("encode error = %v", err)
	}
}

func TestEncodeLegacyObservationRejectsUnrepresentableValidTime(t *testing.T) {
	notMicrosecond := time.Date(2026, time.July, 25, 12, 0, 0, 1, time.UTC)
	later := notMicrosecond.Add(time.Hour)
	instant, err := observation.AtTime(notMicrosecond)
	if err != nil {
		t.Fatalf("create instant: %v", err)
	}
	since, err := observation.Since(notMicrosecond)
	if err != nil {
		t.Fatalf("create since: %v", err)
	}
	duringStart, err := observation.During(notMicrosecond, later)
	if err != nil {
		t.Fatalf("create start interval: %v", err)
	}
	duringEnd, err := observation.During(notMicrosecond.Add(-time.Hour), notMicrosecond)
	if err != nil {
		t.Fatalf("create end interval: %v", err)
	}

	for _, testCase := range []struct {
		name      string
		validTime observation.TemporalExtent
	}{
		{name: "instant", validTime: instant},
		{name: "since_start", validTime: since},
		{name: "during_start", validTime: duringStart},
		{name: "during_end", validTime: duringEnd},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			value := newCodecObservation(t, "compatible_predicate", testCase.validTime, codecRecordedAt)
			_, err := encodeLegacyObservation(value, legacyObservationCompatibility{}, nil, nil)
			if !errors.Is(err, ErrObservationNotRepresentable) {
				t.Fatalf("encode error = %v, want ErrObservationNotRepresentable", err)
			}
		})
	}
}

func newCodecObservation(
	t *testing.T,
	predicate string,
	validTime observation.TemporalExtent,
	recordedAt time.Time,
) observation.Observation {
	t.Helper()
	parsedPredicate, err := observation.NewPredicate(predicate)
	if err != nil {
		t.Fatalf("create predicate: %v", err)
	}
	value, err := observation.NewObservation(observation.ObservationInput{
		ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		Statement: observation.Statement{
			Subject:   observation.AbsentTerm(),
			Predicate: parsedPredicate,
			Object:    observation.AbsentTerm(),
		},
		ValidTime:  validTime,
		RecordedAt: recordedAt,
		Evidence: []observation.EvidenceLink{{
			EvidenceID: evidence.EvidenceID("11111111-2222-3333-4444-555555555555"),
			Role:       observation.EvidenceSupporting,
		}},
		Derivation: observation.Derivation{Method: "codec_test", Version: "v1"},
		Status:     observation.StatusObserved,
	})
	if err != nil {
		t.Fatalf("create observation: %v", err)
	}
	return value
}

func TestLegacyObservationCodecMapsAllTermShapes(t *testing.T) {
	shapes := []struct {
		name                string
		entityID, mentionID string
		wantKind            observation.TermKind
	}{
		{name: "absent", wantKind: observation.TermAbsent},
		{name: "mention", mentionID: "11111111-2222-3333-4444-555555555555", wantKind: observation.TermMention},
		{name: "entity", entityID: "22222222-3333-4444-5555-666666666666", wantKind: observation.TermEntity},
		{name: "entity_with_grounding", entityID: "33333333-4444-5555-6666-777777777777", mentionID: "44444444-5555-6666-7777-888888888888", wantKind: observation.TermEntity},
	}
	for _, subject := range shapes {
		for _, object := range shapes {
			t.Run(subject.name+"_to_"+object.name, func(t *testing.T) {
				row := codecLegacyRow()
				row.SubjectEntityID, row.SubjectMentionID = subject.entityID, subject.mentionID
				row.ObjectEntityID, row.ObjectMentionID = object.entityID, object.mentionID
				decoded, err := decodeLegacyObservation(row, codecOrigin(), nil, codecRun())
				if err != nil {
					t.Fatalf("decode observation: %v", err)
				}
				statement := decoded.Observation.Statement()
				assertCodecTerm(t, "subject", statement.Subject, subject.wantKind, subject.entityID, subject.mentionID)
				assertCodecTerm(t, "object", statement.Object, object.wantKind, object.entityID, object.mentionID)
			})
		}
	}
}

func TestLegacyObservationCodecMapsLegacyValidTime(t *testing.T) {
	start := time.Date(2026, time.July, 20, 11, 0, 0, 0, time.UTC)
	later := start.Add(time.Hour)
	for _, testCase := range []struct {
		name               string
		start, end         *time.Time
		wantKind           observation.TemporalKind
		wantStart, wantEnd time.Time
		hasStart, hasEnd   bool
	}{
		{name: "unknown", wantKind: observation.TemporalUnknown},
		{name: "since", start: codecTimePointer(start), wantKind: observation.TemporalInterval, wantStart: start, hasStart: true},
		{name: "instant", start: codecTimePointer(start), end: codecTimePointer(start), wantKind: observation.TemporalInstant, wantStart: start, hasStart: true},
		{name: "during", start: codecTimePointer(start), end: codecTimePointer(later), wantKind: observation.TemporalInterval, wantStart: start, wantEnd: later, hasStart: true, hasEnd: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			row := codecLegacyRow()
			row.ValidStart, row.ValidEnd = testCase.start, testCase.end
			decoded, err := decodeLegacyObservation(row, codecOrigin(), nil, codecRun())
			if err != nil {
				t.Fatalf("decode observation: %v", err)
			}
			validTime := decoded.Observation.ValidTime()
			if validTime.Kind() != testCase.wantKind {
				t.Fatalf("valid time kind = %v, want %v", validTime.Kind(), testCase.wantKind)
			}
			if instant, ok := validTime.Instant(); ok {
				if !instant.Equal(testCase.wantStart) {
					t.Fatalf("instant = %v, want %v", instant, testCase.wantStart)
				}
				return
			}
			gotStart, hasStart, gotEnd, hasEnd := validTime.Bounds()
			if hasStart != testCase.hasStart || hasEnd != testCase.hasEnd ||
				hasStart && !gotStart.Equal(testCase.wantStart) || hasEnd && !gotEnd.Equal(testCase.wantEnd) {
				t.Fatalf("valid bounds = (%v, %v, %v, %v)", gotStart, hasStart, gotEnd, hasEnd)
			}
		})
	}
}

func TestEncodeLegacyObservationRejectsUnsupportedCanonicalShapes(t *testing.T) {
	textTerm, err := observation.NewTextTerm("private source text")
	if err != nil {
		t.Fatalf("create text term: %v", err)
	}
	until, err := observation.Until(codecRecordedAt)
	if err != nil {
		t.Fatalf("create until: %v", err)
	}
	window, err := observation.Within(codecRecordedAt, codecRecordedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("create window: %v", err)
	}
	unitConfidence, err := observation.NewUnitIntervalConfidence(0.75)
	if err != nil {
		t.Fatalf("create unit confidence: %v", err)
	}
	for _, testCase := range []struct {
		name  string
		value observation.Observation
		run   *owningExtractionRun
		want  error
	}{
		{name: "text", value: codecObservationWith(t, func(input *observation.ObservationInput) { input.Statement.Subject = textTerm }), want: ErrObservationNotRepresentable},
		{name: "until", value: codecObservationWith(t, func(input *observation.ObservationInput) { input.ValidTime = until }), want: ErrObservationNotRepresentable},
		{name: "window", value: codecObservationWith(t, func(input *observation.ObservationInput) { input.ValidTime = window }), want: ErrObservationNotRepresentable},
		{name: "unit_interval_confidence", value: codecObservationWith(t, func(input *observation.ObservationInput) { input.Confidence = &unitConfidence }), want: ErrObservationNotRepresentable},
		{name: "active_uncited", value: codecObservationWith(t, func(input *observation.ObservationInput) { input.Evidence = nil; input.LegacyUncited = true }), want: ErrObservationNotRepresentable},
		{name: "legacy_unversioned", value: codecObservationWith(t, func(input *observation.ObservationInput) {
			input.Derivation = observation.Derivation{Method: "historical", LegacyUnversioned: true}
		}), want: ErrObservationNotRepresentable},
		{name: "missing_owning_run", value: codecObservationWith(t, func(input *observation.ObservationInput) { input.Derivation.RunID = codecRun().ID }), want: ErrObservationCompatibility},
		{name: "model_without_prompt", value: codecObservationWith(t, func(input *observation.ObservationInput) { input.Derivation.RunID = codecRun().ID }), run: &owningExtractionRun{ID: codecRun().ID, ModelID: "model-only"}, want: ErrObservationCompatibility},
		{name: "prompt_without_model", value: codecObservationWith(t, func(input *observation.ObservationInput) { input.Derivation.RunID = codecRun().ID }), run: &owningExtractionRun{ID: codecRun().ID, PromptVersion: "prompt-only"}, want: ErrObservationCompatibility},
		{name: "owning_run_id_mismatch", value: codecObservationWith(t, func(input *observation.ObservationInput) {
			input.Derivation.RunID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		}), run: codecRun(), want: ErrObservationCompatibility},
		{name: "model_mismatch", value: codecObservationWith(t, func(input *observation.ObservationInput) {
			input.Derivation.RunID = codecRun().ID
			input.Derivation.Model = "another-model"
			input.Derivation.PromptVersion = codecRun().PromptVersion
		}), run: codecRun(), want: ErrObservationCompatibility},
		{name: "prompt_version_mismatch", value: codecObservationWith(t, func(input *observation.ObservationInput) {
			input.Derivation.RunID = codecRun().ID
			input.Derivation.Model = codecRun().ModelID
			input.Derivation.PromptVersion = "another-prompt"
		}), run: codecRun(), want: ErrObservationCompatibility},
		{name: "evidence_role_ownership_mismatch", value: codecObservationWith(t, func(input *observation.ObservationInput) { input.Evidence[0].Role = observation.EvidenceContradicting }), run: codecRun(), want: ErrObservationCompatibility},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := encodeLegacyObservation(testCase.value, legacyObservationCompatibility{}, testCase.run, nil)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("encode error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestLegacyObservationCodecPreservesConfidenceWithoutInventingScale(t *testing.T) {
	for _, value := range []float64{-2.5, 0, 0.75, 1, 4.25} {
		decoded := decodeFixtureWithObservationConfidence(t, value)
		confidence, ok := decoded.Observation.Confidence()
		if !ok || confidence.Value() != value ||
			confidence.Scale() != observation.ConfidenceUnspecifiedLegacy {
			t.Fatalf("decoded confidence = (%v, %v)", confidence, ok)
		}
	}

	row := codecLegacyRow()
	signalConfidence := 0.25
	decoded, err := decodeLegacyObservation(row, codecOrigin(), &legacySignalState{
		Input: SignalInput{Confidence: signalConfidence},
	}, codecRun())
	if err != nil {
		t.Fatalf("decode separate signal confidence: %v", err)
	}
	if _, ok := decoded.Observation.Confidence(); ok || decoded.Signal == nil || decoded.Signal.Input.Confidence != signalConfidence {
		t.Fatalf("decoded null observation and signal confidence = %#v", decoded)
	}

	observationConfidence := 4.25
	row.Confidence = &observationConfidence
	decoded, err = decodeLegacyObservation(row, codecOrigin(), &legacySignalState{
		Input: SignalInput{Confidence: signalConfidence},
	}, codecRun())
	if err != nil {
		t.Fatalf("decode unequal confidence values: %v", err)
	}
	confidence, ok := decoded.Observation.Confidence()
	if !ok || confidence.Value() != observationConfidence || decoded.Signal.Input.Confidence != signalConfidence {
		t.Fatalf("decoded unequal confidence values = %#v", decoded)
	}
}

func TestLegacyObservationCodecPreservesEvidencePairsAndPrivateOrigin(t *testing.T) {
	origin := []evidence.EvidenceID{
		"22222222-3333-4444-5555-666666666666",
		"11111111-2222-3333-4444-555555555555",
		"22222222-3333-4444-5555-666666666666",
	}
	signal := &legacySignalState{Evidence: []SignalEvidenceInput{
		{EvidenceSpanID: "11111111-2222-3333-4444-555555555555", Role: "supporting"},
		{EvidenceSpanID: "11111111-2222-3333-4444-555555555555", Role: "contradicting"},
	}}
	decoded, err := decodeLegacyObservation(codecLegacyRow(), origin, signal, codecRun())
	if err != nil {
		t.Fatalf("decode observation: %v", err)
	}
	links := decoded.Observation.EvidenceLinks()
	if len(links) != 3 || links[0] != (observation.EvidenceLink{EvidenceID: "11111111-2222-3333-4444-555555555555", Role: observation.EvidenceContradicting}) ||
		links[1] != (observation.EvidenceLink{EvidenceID: "11111111-2222-3333-4444-555555555555", Role: observation.EvidenceSupporting}) ||
		links[2] != (observation.EvidenceLink{EvidenceID: "22222222-3333-4444-5555-666666666666", Role: observation.EvidenceSupporting}) {
		t.Fatalf("evidence links = %#v", links)
	}
	wantOrigin := []evidence.EvidenceID{
		"11111111-2222-3333-4444-555555555555",
		"22222222-3333-4444-5555-666666666666",
	}
	if !sameEvidenceIDs(decoded.Compatibility.observationEvidenceOrigin, wantOrigin) {
		t.Fatalf("private origin = %#v, want %#v", decoded.Compatibility.observationEvidenceOrigin, wantOrigin)
	}
}

func TestLegacyObservationCodecPreservesExactPredicateAndDerivationBytes(t *testing.T) {
	row := codecLegacyRow()
	row.Predicate = " predicate\twith\nbytes "
	row.Derivation = " derivation\twith\nbytes "
	decoded, err := decodeLegacyObservation(row, codecOrigin(), nil, codecRun())
	if err != nil {
		t.Fatalf("decode observation: %v", err)
	}
	if string(decoded.Observation.Statement().Predicate) != row.Predicate || decoded.Observation.Derivation().Method != row.Derivation {
		t.Fatalf("decoded predicate/derivation = %q/%q", decoded.Observation.Statement().Predicate, decoded.Observation.Derivation().Method)
	}
}

func TestLegacyObservationCodecRecoversDerivationFromOwningRun(t *testing.T) {
	row := codecLegacyRow()
	run := codecRun()
	decoded, err := decodeLegacyObservation(row, codecOrigin(), nil, run)
	if err != nil {
		t.Fatalf("decode observation: %v", err)
	}
	derivation := decoded.Observation.Derivation()
	if derivation.Method != row.Derivation || derivation.Version != run.PromptVersion || derivation.RunID != run.ID || derivation.Model != run.ModelID || derivation.PromptVersion != run.PromptVersion || derivation.LegacyUnversioned {
		t.Fatalf("decoded derivation = %#v", derivation)
	}
}

func TestLegacyObservationCodecRoundTripsNormalizedOriginSet(t *testing.T) {
	origin := []evidence.EvidenceID{
		"22222222-3333-4444-5555-666666666666",
		"11111111-2222-3333-4444-555555555555",
		"22222222-3333-4444-5555-666666666666",
	}
	decoded, err := decodeLegacyObservation(codecLegacyRow(), origin, nil, codecRun())
	if err != nil {
		t.Fatalf("decode observation: %v", err)
	}
	write, err := encodeLegacyObservation(decoded.Observation, decoded.Compatibility, codecRun(), nil)
	if err != nil {
		t.Fatalf("encode observation: %v", err)
	}
	wantOrigin := []evidence.EvidenceID{
		"11111111-2222-3333-4444-555555555555",
		"22222222-3333-4444-5555-666666666666",
	}
	if !sameEvidenceIDs(write.Origin, wantOrigin) {
		t.Fatalf("encoded origin = %#v, want %#v", write.Origin, wantOrigin)
	}
	wantDigest, err := computeObservationDigestV1(legacyObservationWrite{Row: write.Row, Origin: wantOrigin})
	if err != nil {
		t.Fatalf("compute expected digest: %v", err)
	}
	if write.Row.Digest != wantDigest {
		t.Fatalf("encoded digest = %x, want %x", write.Row.Digest, wantDigest)
	}
}

func TestLegacyObservationCodecKeepsExplicitEmptyOriginWithSignalOnlyEvidence(t *testing.T) {
	signal := &legacySignalState{
		Input: SignalInput{
			ID:                "bbbbbbbb-cccc-dddd-eeee-ffffffffffff",
			ObservationID:     "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			Category:          "delegation_autonomy",
			Direction:         "strengthening",
			ExtractionModelID: codecRun().ModelID,
			PromptVersion:     codecRun().PromptVersion,
			Confidence:        0.75,
		},
		Evidence: []SignalEvidenceInput{{EvidenceSpanID: "11111111-2222-3333-4444-555555555555", Role: "supporting"}},
	}
	decoded, err := decodeLegacyObservation(codecLegacyRow(), []evidence.EvidenceID{}, signal, codecRun())
	if err != nil {
		t.Fatalf("decode observation: %v", err)
	}
	if decoded.Compatibility.observationEvidenceOrigin == nil {
		t.Fatal("decoded explicit empty origin became absent metadata")
	}
	write, err := encodeLegacyObservation(decoded.Observation, decoded.Compatibility, codecRun(), decoded.Signal)
	if err != nil {
		t.Fatalf("encode observation: %v", err)
	}
	if write.Origin == nil || len(write.Origin) != 0 {
		t.Fatalf("encoded origin = %#v, want explicit empty set", write.Origin)
	}
	wantDigest, err := computeObservationDigestV1(legacyObservationWrite{Row: write.Row, Origin: []evidence.EvidenceID{}})
	if err != nil {
		t.Fatalf("compute expected digest: %v", err)
	}
	if write.Row.Digest != wantDigest {
		t.Fatalf("encoded digest = %x, want %x", write.Row.Digest, wantDigest)
	}
}

func TestEncodeLegacyObservationRequiresActiveRunIdentityAndProvenance(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		value  observation.Observation
		run    *owningExtractionRun
		wantOK bool
	}{
		{name: "matching_active_observation", value: codecActiveObservation(t, nil), run: codecRun(), wantOK: true},
		{name: "missing_run_id", value: codecObservationWith(t, nil), run: codecRun()},
		{name: "version_mismatch", value: codecActiveObservation(t, func(input *observation.ObservationInput) { input.Derivation.Version = "other-version" }), run: codecRun()},
		{name: "model_mismatch", value: codecActiveObservation(t, func(input *observation.ObservationInput) { input.Derivation.Model = "other-model" }), run: codecRun()},
		{name: "prompt_mismatch", value: codecActiveObservation(t, func(input *observation.ObservationInput) { input.Derivation.PromptVersion = "other-prompt" }), run: codecRun()},
		{name: "recorded_at_mismatch", value: codecActiveObservation(t, func(input *observation.ObservationInput) { input.RecordedAt = codecRecordedAt.Add(time.Microsecond) }), run: codecRun()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := encodeLegacyObservation(testCase.value, legacyObservationCompatibility{}, testCase.run, nil)
			if testCase.wantOK {
				if err != nil {
					t.Fatalf("encode active observation: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrObservationCompatibility) {
				t.Fatalf("encode error = %v, want ErrObservationCompatibility", err)
			}
		})
	}
}

func TestEncodeLegacyObservationValidatesActiveSignalDerivation(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		signal *legacySignalState
		wantOK bool
	}{
		{name: "matching", signal: codecActiveSignal(), wantOK: true},
		{name: "model_mismatch", signal: codecActiveSignalWith(func(input *SignalInput) { input.ExtractionModelID = "other-model" })},
		{name: "prompt_mismatch", signal: codecActiveSignalWith(func(input *SignalInput) { input.PromptVersion = "other-prompt" })},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := encodeLegacyObservation(codecActiveObservation(t, nil), legacyObservationCompatibility{}, codecRun(), testCase.signal)
			if testCase.wantOK {
				if err != nil {
					t.Fatalf("encode observation with signal: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrObservationCompatibility) {
				t.Fatalf("encode error = %v, want ErrObservationCompatibility", err)
			}
		})
	}

	mismatched := codecActiveSignalWith(func(input *SignalInput) { input.ExtractionModelID = "historical-model" })
	decoded, err := decodeLegacyObservation(codecLegacyRow(), codecOrigin(), mismatched, codecRun())
	if err != nil {
		t.Fatalf("decode historical signal state: %v", err)
	}
	if decoded.Signal.Input.ExtractionModelID != "historical-model" || decoded.Observation.Derivation().Model != codecRun().ModelID {
		t.Fatalf("decoded signal/derivation = %#v/%#v", decoded.Signal.Input, decoded.Observation.Derivation())
	}
}

func TestEncodeLegacyObservationBoundsNonUUIDIdentifiers(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		value        observation.Observation
		privateValue string
	}{
		{
			name:         "term",
			privateValue: "private-non-uuid-term",
			value: codecActiveObservation(t, func(input *observation.ObservationInput) {
				term, err := observation.NewEntityTerm("private-non-uuid-term", "")
				if err != nil {
					t.Fatalf("create term: %v", err)
				}
				input.Statement.Subject = term
			}),
		},
		{
			name:         "evidence",
			privateValue: "private-non-uuid-evidence",
			value: codecActiveObservation(t, func(input *observation.ObservationInput) {
				input.Evidence = []observation.EvidenceLink{{EvidenceID: "private-non-uuid-evidence", Role: observation.EvidenceSupporting}}
			}),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := encodeLegacyObservation(testCase.value, legacyObservationCompatibility{}, codecRun(), nil)
			if !errors.Is(err, ErrObservationNotRepresentable) || strings.Contains(err.Error(), testCase.privateValue) {
				t.Fatalf("encode error = %v", err)
			}
		})
	}
}

func TestLegacyObservationCodecAllowsHistoricalUncitedDecodeOnly(t *testing.T) {
	row := codecLegacyRow()
	row.ExtractionRunID = ""
	decoded, err := decodeLegacyObservation(row, nil, nil, nil)
	if err != nil {
		t.Fatalf("decode historical observation: %v", err)
	}
	if !decoded.Observation.LegacyUncited() || len(decoded.Observation.EvidenceLinks()) != 0 || !decoded.Observation.Derivation().LegacyUnversioned {
		t.Fatalf("decoded historical observation = %#v", decoded.Observation)
	}
	if _, err := encodeLegacyObservation(decoded.Observation, decoded.Compatibility, nil, nil); !errors.Is(err, ErrObservationNotRepresentable) {
		t.Fatalf("encode historical observation error = %v", err)
	}
}

func decodeFixtureWithObservationConfidence(t *testing.T, value float64) decodedLegacyObservation {
	t.Helper()
	row := codecLegacyRow()
	row.Confidence = &value
	decoded, err := decodeLegacyObservation(row, codecOrigin(), nil, codecRun())
	if err != nil {
		t.Fatalf("decode observation confidence: %v", err)
	}
	return decoded
}

var codecRecordedAt = time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)

func codecLegacyRow() legacyObservationRow {
	return legacyObservationRow{
		ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", ExtractionRunID: codecRun().ID,
		Predicate: "codec_predicate", Derivation: "codec_derivation", EpistemicStatus: string(observation.StatusObserved),
		RecordedAt: codecRecordedAt,
	}
}

func codecRun() *owningExtractionRun {
	return &owningExtractionRun{
		ID: "99999999-aaaa-bbbb-cccc-dddddddddddd", ModelID: "codec-model", PromptVersion: "codec-prompt-v1", RecordedAt: codecRecordedAt,
	}
}

func codecOrigin() []evidence.EvidenceID {
	return []evidence.EvidenceID{"11111111-2222-3333-4444-555555555555"}
}

func codecTimePointer(value time.Time) *time.Time { return &value }

func codecObservationWith(t *testing.T, mutate func(*observation.ObservationInput)) observation.Observation {
	t.Helper()
	predicate, err := observation.NewPredicate("codec_predicate")
	if err != nil {
		t.Fatalf("create predicate: %v", err)
	}
	input := observation.ObservationInput{
		ID:        "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		Statement: observation.Statement{Subject: observation.AbsentTerm(), Predicate: predicate, Object: observation.AbsentTerm()},
		ValidTime: observation.UnknownTime(), RecordedAt: codecRecordedAt,
		Evidence:   []observation.EvidenceLink{{EvidenceID: codecOrigin()[0], Role: observation.EvidenceSupporting}},
		Derivation: observation.Derivation{Method: "codec_derivation", Version: "v1"}, Status: observation.StatusObserved,
	}
	if mutate != nil {
		mutate(&input)
	}
	value, err := observation.NewObservation(input)
	if err != nil {
		t.Fatalf("create mutated observation: %v", err)
	}
	return value
}

func codecActiveObservation(t *testing.T, mutate func(*observation.ObservationInput)) observation.Observation {
	t.Helper()
	run := codecRun()
	value := codecObservationWith(t, func(input *observation.ObservationInput) {
		input.Derivation = observation.Derivation{
			Method:        "codec_derivation",
			Version:       run.PromptVersion,
			RunID:         run.ID,
			Model:         run.ModelID,
			PromptVersion: run.PromptVersion,
		}
		if mutate != nil {
			mutate(input)
		}
	})
	return value
}

func codecActiveSignal() *legacySignalState {
	return codecActiveSignalWith(nil)
}

func codecActiveSignalWith(mutate func(*SignalInput)) *legacySignalState {
	run := codecRun()
	input := SignalInput{
		ID:                "bbbbbbbb-cccc-dddd-eeee-ffffffffffff",
		ObservationID:     "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		Category:          "delegation_autonomy",
		Direction:         "strengthening",
		ExtractionModelID: run.ModelID,
		PromptVersion:     run.PromptVersion,
		Confidence:        0.75,
	}
	if mutate != nil {
		mutate(&input)
	}
	return &legacySignalState{Input: input, Evidence: []SignalEvidenceInput{{
		EvidenceSpanID: "11111111-2222-3333-4444-555555555555", Role: "supporting",
	}}}
}

func sameEvidenceIDs(left, right []evidence.EvidenceID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func assertCodecTerm(t *testing.T, name string, term observation.Term, wantKind observation.TermKind, wantEntityID, wantMentionID string) {
	t.Helper()
	if term.Kind() != wantKind {
		t.Fatalf("%s kind = %v, want %v", name, term.Kind(), wantKind)
	}
	switch wantKind {
	case observation.TermMention:
		mentionID, ok := term.MentionID()
		if !ok || mentionID != wantMentionID {
			t.Fatalf("%s mention = %q, %v", name, mentionID, ok)
		}
	case observation.TermEntity:
		entityID, mentionID, ok := term.Entity()
		if !ok || entityID != wantEntityID || mentionID != wantMentionID {
			t.Fatalf("%s entity = %q, %q, %v", name, entityID, mentionID, ok)
		}
	}
}

func TestComputeObservationDigestV1GoldenBytes(t *testing.T) {
	instant := codecRecordedAt
	later := instant.Add(time.Hour)
	confidence := 0.75
	negativeConfidence := -2.5
	for _, testCase := range []struct {
		name  string
		write legacyObservationWrite
		want  string
	}{
		{
			name: "nullable_ids_unknown_time_absent_confidence_one_origin",
			write: legacyObservationWrite{Row: legacyObservationRow{
				Predicate: "digest_predicate", Derivation: "digest_derivation", EpistemicStatus: "observed",
			}, Origin: []evidence.EvidenceID{"11111111-2222-3333-4444-555555555555"}},
			want: "a72e93d7875565758e279853dc76a33f1dd93165b10031def147f2be23ad7efc",
		},
		{
			name: "entity_grounding_instant_confidence_duplicate_origins",
			write: legacyObservationWrite{Row: legacyObservationRow{
				SubjectEntityID: "22222222-3333-4444-5555-666666666666", SubjectMentionID: "33333333-4444-5555-6666-777777777777",
				Predicate: "digest_predicate", Derivation: "digest_derivation", EpistemicStatus: "observed",
				ValidStart: &instant, ValidEnd: &instant, Confidence: &confidence,
			}, Origin: []evidence.EvidenceID{
				"99999999-AAAA-BBBB-CCCC-DDDDDDDDDDDD", "11111111-2222-3333-4444-555555555555", "99999999-aaaa-bbbb-cccc-dddddddddddd",
			}},
			want: "7c94b1eb4b6792f4f796bad686b78368f23e5c80db00b34fafcc676dda13168d",
		},
		{
			name: "extraction_run_bounded_interval_negative_confidence_no_origin",
			write: legacyObservationWrite{Row: legacyObservationRow{
				ExtractionRunID: codecRun().ID, Predicate: "digest_predicate", Derivation: "digest_derivation", EpistemicStatus: "observed",
				ValidStart: &instant, ValidEnd: &later, Confidence: &negativeConfidence,
			}},
			want: "997ca736c404491fc36146dc75841ff8eb69f58bb050f9bb194aa8efb7e1528c",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := computeObservationDigestV1(testCase.write)
			if err != nil {
				t.Fatalf("compute digest: %v", err)
			}
			if hex.EncodeToString(got[:]) != testCase.want {
				t.Fatalf("digest = %x, want %s", got, testCase.want)
			}
		})
	}
}

func TestComputeObservationDigestV1IgnoresCanonicalFieldsAbsentFromV1(t *testing.T) {
	base := codecDigestWrite()
	baseline, err := computeObservationDigestV1(base)
	if err != nil {
		t.Fatalf("compute baseline digest: %v", err)
	}
	changed := base
	changed.Row.RecordedAt = codecRecordedAt.Add(time.Hour)
	changed.Signal = &legacySignalState{Evidence: []SignalEvidenceInput{
		{EvidenceSpanID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Role: "supporting"},
		{EvidenceSpanID: "bbbbbbbb-cccc-dddd-eeee-ffffffffffff", Role: "contradicting"},
	}}
	got, err := computeObservationDigestV1(changed)
	if err != nil {
		t.Fatalf("compute changed digest: %v", err)
	}
	if got != baseline {
		t.Fatal("recorded time and signal-only evidence changed v1 digest")
	}
}

func TestComputeObservationDigestV1ChangesOnlyWithLegacyOrigin(t *testing.T) {
	base := codecDigestWrite()
	baseline, err := computeObservationDigestV1(base)
	if err != nil {
		t.Fatalf("compute baseline digest: %v", err)
	}
	changed := base
	changed.Origin = append(changed.Origin, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	got, err := computeObservationDigestV1(changed)
	if err != nil {
		t.Fatalf("compute changed digest: %v", err)
	}
	if got == baseline {
		t.Fatal("additional legacy origin did not change v1 digest")
	}
}

func TestComputeObservationDigestV1MatchesOldWriterDigest(t *testing.T) {
	validStart := codecRecordedAt
	validEnd := validStart.Add(time.Hour)
	confidence := 0.75
	input := ObservationInput{
		ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", ExtractionRunID: "99999999-aaaa-bbbb-cccc-dddddddddddd",
		SubjectEntityID: "22222222-3333-4444-5555-666666666666", ObjectEntityID: "33333333-4444-5555-6666-777777777777",
		SubjectMentionID: "44444444-5555-6666-7777-888888888888", ObjectMentionID: "55555555-6666-7777-8888-999999999999",
		Predicate: "digest_predicate", Derivation: "digest_derivation", EpistemicStatus: "observed",
		ValidStart: &validStart, ValidEnd: &validEnd, Confidence: &confidence,
	}
	origin := []string{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "11111111-2222-3333-4444-555555555555", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}
	oldDigest, err := ComputeObservationDigest(input, origin)
	if err != nil {
		t.Fatalf("compute old writer digest: %v", err)
	}
	newDigest, err := computeObservationDigestV1(legacyObservationWrite{Row: legacyObservationRow{
		ExtractionRunID: input.ExtractionRunID, SubjectEntityID: input.SubjectEntityID, ObjectEntityID: input.ObjectEntityID,
		SubjectMentionID: input.SubjectMentionID, ObjectMentionID: input.ObjectMentionID, Predicate: input.Predicate,
		Derivation: input.Derivation, EpistemicStatus: input.EpistemicStatus, ValidStart: input.ValidStart, ValidEnd: input.ValidEnd,
		Confidence: input.Confidence,
	}, Origin: []evidence.EvidenceID{evidence.EvidenceID(origin[0]), evidence.EvidenceID(origin[1]), evidence.EvidenceID(origin[2])}})
	if err != nil {
		t.Fatalf("compute v1 digest: %v", err)
	}
	if newDigest != oldDigest {
		t.Fatalf("new v1 digest = %x, old writer digest = %x", newDigest, oldDigest)
	}
}

func codecDigestWrite() legacyObservationWrite {
	confidence := 0.75
	return legacyObservationWrite{Row: legacyObservationRow{
		ExtractionRunID: codecRun().ID, SubjectEntityID: "22222222-3333-4444-5555-666666666666",
		ObjectEntityID: "33333333-4444-5555-6666-777777777777", Predicate: "digest_predicate",
		Derivation: "digest_derivation", EpistemicStatus: "observed", Confidence: &confidence,
	}, Origin: []evidence.EvidenceID{"11111111-2222-3333-4444-555555555555"}}
}
