package storage

import (
	"errors"
	"testing"

	knowledge "github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"

	"stacks/internal/ingest"
	"stacks/internal/modelpolicy"
)

func TestLegacyIngestionValidationPreservesPreflightInvariants(t *testing.T) {
	testCases := []struct {
		name   string
		mutate func(*legacyIngestionCompletion)
		want   error
	}{
		{
			name: "duplicate evidence identity",
			mutate: func(completion *legacyIngestionCompletion) {
				completion.Evidence = append(completion.Evidence, completion.Evidence[0])
			},
			want: ingest.ErrPersistenceCollision,
		},
		{
			name: "ungrounded normalized mention",
			mutate: func(completion *legacyIngestionCompletion) {
				completion.Mentions[0].NormalizedName = "different person"
			},
			want: ingest.ErrPersistenceReference,
		},
		{
			name: "invalid observation confidence",
			mutate: func(completion *legacyIngestionCompletion) {
				completion.Observations[0].SourceConfidence = observation.Confidence{}
			},
			want: ingest.ErrPersistenceReference,
		},
		{
			name: "missing observation evidence",
			mutate: func(completion *legacyIngestionCompletion) {
				completion.Observations[0].EvidenceKeys = []string{"missing-evidence"}
			},
			want: ingest.ErrPersistenceReference,
		},
		{
			name: "duplicate signal evidence role",
			mutate: func(completion *legacyIngestionCompletion) {
				completion.Signals[0].Evidence = append(
					completion.Signals[0].Evidence,
					completion.Signals[0].Evidence[0],
				)
			},
			want: ingest.ErrPersistenceCollision,
		},
		{
			name: "signal references missing observation",
			mutate: func(completion *legacyIngestionCompletion) {
				completion.Signals[0].ObservationID = "missing-observation"
			},
			want: ingest.ErrPersistenceReference,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			completion := validLegacyIngestionCompletion(t)
			testCase.mutate(&completion)

			if err := validateLegacyForPersistence(completion); !errors.Is(err, testCase.want) {
				t.Fatalf("validateLegacyForPersistence() error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func validLegacyIngestionCompletion(t *testing.T) legacyIngestionCompletion {
	t.Helper()
	const transcript = "Synthetic Person assigned follow-up."
	version := testIngestionDocumentVersion(
		t,
		"legacy-validator",
		"legacy-validator-version",
		transcript,
	)
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, SectionID: "tab-synthetic",
		StartOffset: 0, EndOffset: len(transcript), Quote: transcript,
		RecordedAt: version.RecordedAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	confidence, err := observation.NewUnitIntervalConfidence(0.8)
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := observation.NewPredicate("interaction_signal")
	if err != nil {
		t.Fatal(err)
	}
	observationID := observation.ObservationID("legacy-observation")
	return legacyIngestionCompletion{
		VersionID: "legacy-version", DerivationID: "legacy-run",
		LeaseOwner: "legacy-owner", DataMode: modelpolicy.DataModePersonal,
		Evidence: []ingest.EvidenceRecord{{Key: "citation", Span: span}},
		Mentions: []ingest.MentionRecord{{
			Key: "person", EvidenceKey: "citation", Surface: "Synthetic Person",
			NormalizedName: "synthetic person", Role: "speaker",
		}},
		Observations: []legacyObservationDraft{{
			ID: observationID, SubjectMentionKey: "person", Predicate: predicate,
			ValidTime: observation.UnknownTime(), RecordedAt: version.RecordedAt(),
			EvidenceKeys: []string{"citation"}, SourceConfidence: confidence,
		}},
		Signals: []legacySignalRecord{{
			ID: "legacy-signal", ObservationID: string(observationID),
			Category: "delegation_autonomy", Direction: "strengthening",
			ExtractionModelID: "synthetic-model", PromptVersion: "extract-v2",
			Rationale: "Synthetic legacy signal.", Confidence: 0.8,
			Evidence: []legacySignalEvidenceRecord{{
				EvidenceKey: "citation", Role: "supporting",
			}},
		}},
	}
}
