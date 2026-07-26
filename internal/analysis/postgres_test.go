package analysis

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
)

func TestCanonicalManagerQueryRequiresAcceptedPair(t *testing.T) {
	loader := &fakeRelationshipSnapshotLoader{
		snapshots: []postgres.RelationshipSnapshot{{
			SubjectAccepted: false,
			ObjectAccepted:  true,
			Observations: []postgres.ObservationRecord{
				canonicalManagerRecord(
					t,
					"observation:should-not-map",
					"stacks.interaction.v1/delegation_autonomy/strengthening",
					observation.UnknownTime(),
					[]canonicalCitationInput{supportingTranscriptCitation("evidence:pair")},
				),
			},
		}},
	}
	repository := PostgresRepository{Database: loader}

	snapshot, err := repository.LoadPairInputs(
		context.Background(),
		"employee:opaque/id",
		"manager:opaque/id",
	)
	if err != nil {
		t.Fatalf("LoadPairInputs() error = %v", err)
	}
	if snapshot.Accepted || len(snapshot.Signals) != 0 {
		t.Fatalf("LoadPairInputs() = %#v, want rejected pair with no signals", snapshot)
	}
	if loader.subjectID != "manager:opaque/id" || loader.objectID != "employee:opaque/id" {
		t.Fatalf(
			"relationship order = %q -> %q, want manager -> employee",
			loader.subjectID,
			loader.objectID,
		)
	}
}

func TestCanonicalManagerQueryMapsOnlyVersionedInteractionPredicates(t *testing.T) {
	loader := &fakeRelationshipSnapshotLoader{
		snapshots: []postgres.RelationshipSnapshot{{
			SubjectAccepted: true,
			ObjectAccepted:  true,
			Observations: []postgres.ObservationRecord{
				canonicalManagerRecord(
					t,
					"observation:manager-signal",
					"stacks.interaction.v1/delegation_autonomy/strengthening",
					observation.UnknownTime(),
					[]canonicalCitationInput{supportingTranscriptCitation("evidence:manager")},
				),
				canonicalManagerRecord(
					t,
					"observation:unrelated",
					"project.commitment.changed",
					observation.UnknownTime(),
					[]canonicalCitationInput{supportingTranscriptCitation("evidence:unrelated")},
				),
				canonicalManagerRecord(
					t,
					"observation:other-version",
					"stacks.interaction.v2/delegation_autonomy/strengthening",
					observation.UnknownTime(),
					[]canonicalCitationInput{supportingTranscriptCitation("evidence:other-version")},
				),
			},
		}},
	}

	snapshot, err := (PostgresRepository{Database: loader}).LoadPairInputs(
		context.Background(),
		"employee:opaque/id",
		"manager:opaque/id",
	)
	if err != nil {
		t.Fatalf("LoadPairInputs() error = %v", err)
	}
	if len(snapshot.Signals) != 1 {
		t.Fatalf("signal count = %d, want only the current interaction predicate", len(snapshot.Signals))
	}
	signal := snapshot.Signals[0]
	if signal.ID != "observation:manager-signal" ||
		signal.ObservationID != "observation:manager-signal" ||
		signal.Category != CategoryDelegationAutonomy ||
		signal.Direction != DirectionStrengthening ||
		signal.Rationale != ExplainSignal(CategoryDelegationAutonomy, DirectionStrengthening) {
		t.Fatalf("mapped signal = %#v", signal)
	}
}

func TestCanonicalManagerQueryPreservesSupportingAndCounterevidence(t *testing.T) {
	supporting := supportingTranscriptCitation("evidence:supporting")
	supporting.sourceDocumentID = "source:meeting-one"
	supporting.providerDocumentID = "provider-doc-one"
	supporting.sectionID = "transcript-tab"
	supporting.sectionRole = "transcript"
	supporting.locator = "https://example.invalid/document/provider-doc-one#transcript-tab"
	counter := canonicalCitationInput{
		id:                 "evidence:counter",
		role:               observation.EvidenceContradicting,
		sourceDocumentID:   "source:meeting-one",
		providerDocumentID: "provider-doc-one",
		sectionID:          "notes-tab",
		sectionRole:        "gemini-notes",
		locator:            "https://example.invalid/document/provider-doc-one#notes-tab",
		quote:              "Synthetic counterevidence.",
	}
	record := canonicalManagerRecord(
		t,
		"observation:evidence-roles",
		"stacks.interaction.v1/scrutiny_correction/weakening",
		observation.UnknownTime(),
		[]canonicalCitationInput{counter, supporting},
	)
	loader := &fakeRelationshipSnapshotLoader{
		snapshots: []postgres.RelationshipSnapshot{{
			SubjectAccepted: true,
			ObjectAccepted:  true,
			Observations:    []postgres.ObservationRecord{record},
		}},
	}

	snapshot, err := (PostgresRepository{Database: loader}).LoadPairInputs(
		context.Background(),
		"employee:opaque/id",
		"manager:opaque/id",
	)
	if err != nil {
		t.Fatalf("LoadPairInputs() error = %v", err)
	}
	signal := snapshot.Signals[0]
	if !signal.TranscriptBacked || signal.MeetingID != "source:meeting-one" {
		t.Fatalf(
			"transcript/meeting = %t/%q, want true/source:meeting-one",
			signal.TranscriptBacked,
			signal.MeetingID,
		)
	}
	if len(signal.Citations) != 2 {
		t.Fatalf("citation count = %d, want 2", len(signal.Citations))
	}
	gotRoles := []CitationRole{signal.Citations[0].Role, signal.Citations[1].Role}
	slices.Sort(gotRoles)
	if !slices.Equal(gotRoles, []CitationRole{CitationContradicting, CitationSupporting}) {
		t.Fatalf("citation roles = %#v", gotRoles)
	}
	for _, citation := range signal.Citations {
		if citation.ID == "" ||
			citation.ProviderDocumentID != "provider-doc-one" ||
			citation.ProviderTabID == "" ||
			citation.EndOffset <= citation.StartOffset ||
			citation.Quote == "" ||
			citation.Locator == "" ||
			citation.SectionRole == "" {
			t.Fatalf("incomplete canonical citation = %#v", citation)
		}
	}

	secondDocument := supporting
	secondDocument.id = "evidence:second-document"
	secondDocument.sourceDocumentID = "source:meeting-two"
	secondDocument.providerDocumentID = "provider-doc-two"
	loader.snapshots = []postgres.RelationshipSnapshot{{
		SubjectAccepted: true,
		ObjectAccepted:  true,
		Observations: []postgres.ObservationRecord{canonicalManagerRecord(
			t,
			"observation:multi-document",
			"stacks.interaction.v1/scrutiny_correction/weakening",
			observation.UnknownTime(),
			[]canonicalCitationInput{supporting, secondDocument},
		)},
	}}
	snapshot, err = (PostgresRepository{Database: loader}).LoadPairInputs(
		context.Background(),
		"employee:opaque/id",
		"manager:opaque/id",
	)
	if err != nil {
		t.Fatalf("LoadPairInputs(multi-document) error = %v", err)
	}
	if !snapshot.Signals[0].TranscriptBacked || snapshot.Signals[0].MeetingID != "" {
		t.Fatalf(
			"multi-document signal transcript/meeting = %t/%q, want true/empty",
			snapshot.Signals[0].TranscriptBacked,
			snapshot.Signals[0].MeetingID,
		)
	}
}

func TestCanonicalManagerQuerySeparatesUnknownAndDatedSourceTime(t *testing.T) {
	sourceTime := time.Date(2026, time.July, 8, 9, 30, 0, 0, time.UTC)
	instant, err := observation.AtTime(sourceTime)
	if err != nil {
		t.Fatalf("observation.AtTime() error = %v", err)
	}
	unknownRecord := canonicalManagerRecord(
		t,
		"observation:unknown-time",
		"stacks.interaction.v1/support_advocacy/unclear",
		observation.UnknownTime(),
		[]canonicalCitationInput{supportingTranscriptCitation("evidence:unknown")},
	)
	datedRecord := canonicalManagerRecord(
		t,
		"observation:dated-time",
		"stacks.interaction.v1/support_advocacy/strengthening",
		instant,
		[]canonicalCitationInput{supportingTranscriptCitation("evidence:dated")},
	)
	loader := &fakeRelationshipSnapshotLoader{
		snapshots: []postgres.RelationshipSnapshot{{
			SubjectAccepted: true,
			ObjectAccepted:  true,
			Observations:    []postgres.ObservationRecord{unknownRecord, datedRecord},
		}},
	}

	snapshot, err := (PostgresRepository{Database: loader}).LoadPairInputs(
		context.Background(),
		"employee:opaque/id",
		"manager:opaque/id",
	)
	if err != nil {
		t.Fatalf("LoadPairInputs() error = %v", err)
	}
	byID := make(map[string]Signal, len(snapshot.Signals))
	for _, signal := range snapshot.Signals {
		byID[signal.ID] = signal
	}
	unknownSignal := byID["observation:unknown-time"]
	datedSignal := byID["observation:dated-time"]
	if unknownSignal.ValidTime != nil {
		t.Fatalf("unknown valid time = %v, want nil", unknownSignal.ValidTime)
	}
	if datedSignal.ValidTime == nil ||
		!datedSignal.ValidTime.Equal(sourceTime) {
		t.Fatalf("dated valid time = %v, want %v", datedSignal.ValidTime, sourceTime)
	}
	if unknownSignal.RecordedAt != unknownRecord.Observation.RecordedAt() ||
		datedSignal.RecordedAt != datedRecord.Observation.RecordedAt() {
		t.Fatal("recorded time was not preserved independently")
	}

	interval, err := observation.Since(sourceTime)
	if err != nil {
		t.Fatalf("observation.Since() error = %v", err)
	}
	loader.snapshots = []postgres.RelationshipSnapshot{{
		SubjectAccepted: true,
		ObjectAccepted:  true,
		Observations: []postgres.ObservationRecord{canonicalManagerRecord(
			t,
			"observation:interval",
			"stacks.interaction.v1/support_advocacy/strengthening",
			interval,
			[]canonicalCitationInput{supportingTranscriptCitation("evidence:interval")},
		)},
	}}
	if _, err := (PostgresRepository{Database: loader}).LoadPairInputs(
		context.Background(),
		"employee:opaque/id",
		"manager:opaque/id",
	); err == nil || !strings.Contains(err.Error(), "temporal shape") {
		t.Fatalf("LoadPairInputs(interval) error = %v, want bounded temporal-shape error", err)
	}
}

func TestCanonicalManagerQueryUsesCurrentAdmissionAndIdentityAuthority(t *testing.T) {
	loader := &fakeRelationshipSnapshotLoader{
		snapshots: []postgres.RelationshipSnapshot{
			{
				SubjectAccepted: false,
				ObjectAccepted:  true,
			},
			{
				SubjectAccepted: true,
				ObjectAccepted:  true,
				Observations: []postgres.ObservationRecord{canonicalManagerRecord(
					t,
					"observation:currently-admitted",
					"stacks.interaction.v1/future_responsibility/strengthening",
					observation.UnknownTime(),
					[]canonicalCitationInput{supportingTranscriptCitation("evidence:current")},
				)},
			},
		},
	}
	repository := PostgresRepository{Database: loader}

	before, err := repository.LoadPairInputs(
		context.Background(),
		"employee:opaque/id",
		"manager:opaque/id",
	)
	if err != nil {
		t.Fatalf("first LoadPairInputs() error = %v", err)
	}
	after, err := repository.LoadPairInputs(
		context.Background(),
		"employee:opaque/id",
		"manager:opaque/id",
	)
	if err != nil {
		t.Fatalf("second LoadPairInputs() error = %v", err)
	}
	if before.Accepted || len(before.Signals) != 0 ||
		!after.Accepted || len(after.Signals) != 1 {
		t.Fatalf("before/after snapshots = %#v / %#v", before, after)
	}
}

func TestCanonicalManagerQueryCorrectionChangesResultWithoutReingest(t *testing.T) {
	record := canonicalManagerRecord(
		t,
		"observation:immutable",
		"stacks.interaction.v1/endorsement_trust/strengthening",
		observation.UnknownTime(),
		[]canonicalCitationInput{supportingTranscriptCitation("evidence:immutable")},
	)
	loader := &fakeRelationshipSnapshotLoader{
		snapshots: []postgres.RelationshipSnapshot{
			{SubjectAccepted: true, ObjectAccepted: true, Observations: []postgres.ObservationRecord{record}},
			{SubjectAccepted: true, ObjectAccepted: true},
		},
	}
	repository := PostgresRepository{Database: loader}

	before, err := repository.LoadPairInputs(
		context.Background(),
		"employee:opaque/id",
		"manager:opaque/id",
	)
	if err != nil {
		t.Fatalf("first LoadPairInputs() error = %v", err)
	}
	after, err := repository.LoadPairInputs(
		context.Background(),
		"employee:opaque/id",
		"manager:opaque/id",
	)
	if err != nil {
		t.Fatalf("second LoadPairInputs() error = %v", err)
	}
	if len(before.Signals) != 1 || len(after.Signals) != 0 ||
		before.Signals[0].ObservationID != string(record.Observation.ID()) {
		t.Fatalf("before/after signals = %#v / %#v", before.Signals, after.Signals)
	}
}

func TestCanonicalManagerQueryAcceptsOpaqueEntityIDs(t *testing.T) {
	loader := &fakeRelationshipSnapshotLoader{
		snapshots: []postgres.RelationshipSnapshot{{
			SubjectAccepted: true,
			ObjectAccepted:  true,
		}},
	}
	repository := PostgresRepository{Database: loader}

	snapshot, err := repository.LoadPairInputs(
		context.Background(),
		" employee:team/alpha ",
		" manager:team/beta ",
	)
	if err != nil {
		t.Fatalf("LoadPairInputs() error = %v", err)
	}
	if !snapshot.Accepted ||
		loader.subjectID != "manager:team/beta" ||
		loader.objectID != "employee:team/alpha" {
		t.Fatalf(
			"snapshot/order = %#v, %q -> %q",
			snapshot,
			loader.subjectID,
			loader.objectID,
		)
	}
	for name, pair := range map[string][2]string{
		"blank employee":  {" ", "manager:team/beta"},
		"blank manager":   {"employee:team/alpha", " "},
		"same after trim": {" entity:same ", "entity:same"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := repository.LoadPairInputs(
				context.Background(),
				pair[0],
				pair[1],
			); err == nil {
				t.Fatal("LoadPairInputs() error = nil")
			}
		})
	}
}

func TestCanonicalManagerQueryResolvesMentionTermsThroughCurrentAuthority(t *testing.T) {
	record := canonicalManagerRecord(
		t,
		"observation:mention-grounded",
		"stacks.interaction.v1/delegation_autonomy/weakening",
		observation.UnknownTime(),
		[]canonicalCitationInput{supportingTranscriptCitation("evidence:mention-grounded")},
	)
	record.SubjectEntityID = identity.EntityID("manager:corrected")
	record.ObjectEntityID = identity.EntityID("employee:opaque/id")
	loader := &fakeRelationshipSnapshotLoader{
		snapshots: []postgres.RelationshipSnapshot{
			{SubjectAccepted: true, ObjectAccepted: true},
			{SubjectAccepted: true, ObjectAccepted: true, Observations: []postgres.ObservationRecord{record}},
		},
	}
	repository := PostgresRepository{Database: loader}

	before, err := repository.LoadPairInputs(
		context.Background(),
		"employee:opaque/id",
		"manager:original",
	)
	if err != nil {
		t.Fatalf("original LoadPairInputs() error = %v", err)
	}
	after, err := repository.LoadPairInputs(
		context.Background(),
		"employee:opaque/id",
		"manager:corrected",
	)
	if err != nil {
		t.Fatalf("corrected LoadPairInputs() error = %v", err)
	}
	if len(before.Signals) != 0 || len(after.Signals) != 1 {
		t.Fatalf("original/corrected signals = %#v / %#v", before.Signals, after.Signals)
	}
}

func TestCanonicalManagerQueryRejectsMissingOrNonUnitConfidence(t *testing.T) {
	record := canonicalManagerRecord(
		t,
		"observation:missing-confidence",
		"stacks.interaction.v1/delegation_autonomy/strengthening",
		observation.UnknownTime(),
		[]canonicalCitationInput{supportingTranscriptCitation("evidence:missing-confidence")},
	)
	record.Observation = canonicalObservationWithConfidence(
		t,
		"observation:missing-confidence",
		"stacks.interaction.v1/delegation_autonomy/strengthening",
		observation.UnknownTime(),
		record.Observation.EvidenceLinks(),
		nil,
	)
	loader := &fakeRelationshipSnapshotLoader{
		snapshots: []postgres.RelationshipSnapshot{{
			SubjectAccepted: true,
			ObjectAccepted:  true,
			Observations:    []postgres.ObservationRecord{record},
		}},
	}
	if _, err := (PostgresRepository{Database: loader}).LoadPairInputs(
		context.Background(),
		"employee:opaque/id",
		"manager:opaque/id",
	); err == nil || !strings.Contains(err.Error(), "confidence") {
		t.Fatalf("LoadPairInputs() error = %v, want bounded confidence error", err)
	}
}

type fakeRelationshipSnapshotLoader struct {
	snapshots []postgres.RelationshipSnapshot
	err       error
	calls     int
	subjectID identity.EntityID
	objectID  identity.EntityID
}

func (loader *fakeRelationshipSnapshotLoader) LoadRelationshipSnapshot(
	_ context.Context,
	subjectID identity.EntityID,
	objectID identity.EntityID,
) (postgres.RelationshipSnapshot, error) {
	loader.calls++
	loader.subjectID = subjectID
	loader.objectID = objectID
	if loader.err != nil {
		return postgres.RelationshipSnapshot{}, loader.err
	}
	if len(loader.snapshots) == 0 {
		return postgres.RelationshipSnapshot{}, nil
	}
	snapshot := loader.snapshots[0]
	loader.snapshots = loader.snapshots[1:]
	return snapshot, nil
}

type canonicalCitationInput struct {
	id                 string
	role               observation.EvidenceRole
	sourceDocumentID   string
	providerDocumentID string
	sectionID          string
	sectionRole        string
	locator            string
	quote              string
}

func supportingTranscriptCitation(id string) canonicalCitationInput {
	return canonicalCitationInput{
		id:                 id,
		role:               observation.EvidenceSupporting,
		sourceDocumentID:   "source:" + id,
		providerDocumentID: "provider:" + id,
		sectionID:          "transcript",
		sectionRole:        "transcript",
		locator:            "https://example.invalid/" + id,
		quote:              "Synthetic supporting evidence.",
	}
}

func canonicalManagerRecord(
	t *testing.T,
	id string,
	predicate string,
	validTime observation.TemporalExtent,
	citations []canonicalCitationInput,
) postgres.ObservationRecord {
	t.Helper()
	links := make([]observation.EvidenceLink, len(citations))
	evidenceRecords := make([]postgres.ObservationEvidenceRecord, len(citations))
	for index, citation := range citations {
		span := canonicalEvidenceSpan(t, citation)
		links[index] = observation.EvidenceLink{
			EvidenceID: span.ID(),
			Role:       citation.role,
		}
		evidenceRecords[index] = postgres.ObservationEvidenceRecord{
			Span:             span,
			Role:             citation.role,
			SourceDocumentID: citation.sourceDocumentID,
			SectionID:        citation.sectionID,
			SectionRole:      citation.sectionRole,
		}
	}
	confidence, err := observation.NewUnitIntervalConfidence(0.75)
	if err != nil {
		t.Fatalf("observation.NewUnitIntervalConfidence() error = %v", err)
	}
	return postgres.ObservationRecord{
		Observation: canonicalObservationWithConfidence(
			t,
			id,
			predicate,
			validTime,
			links,
			&confidence,
		),
		Evidence:        evidenceRecords,
		SubjectEntityID: "manager:opaque/id",
		ObjectEntityID:  "employee:opaque/id",
	}
}

func canonicalObservationWithConfidence(
	t *testing.T,
	id string,
	predicateValue string,
	validTime observation.TemporalExtent,
	links []observation.EvidenceLink,
	confidence *observation.Confidence,
) observation.Observation {
	t.Helper()
	subject, err := observation.NewEntityTerm("manager:opaque/id", "")
	if err != nil {
		t.Fatalf("observation.NewEntityTerm(subject) error = %v", err)
	}
	object, err := observation.NewEntityTerm("employee:opaque/id", "")
	if err != nil {
		t.Fatalf("observation.NewEntityTerm(object) error = %v", err)
	}
	predicate, err := observation.NewPredicate(predicateValue)
	if err != nil {
		t.Fatalf("observation.NewPredicate() error = %v", err)
	}
	value, err := observation.NewObservation(observation.ObservationInput{
		ID: observation.ObservationID(id),
		Statement: observation.Statement{
			Subject:   subject,
			Predicate: predicate,
			Object:    object,
		},
		ValidTime:  validTime,
		RecordedAt: time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC),
		Evidence:   links,
		Derivation: observation.Derivation{
			Method:        "structured-extraction",
			Version:       "extract-v2",
			RunID:         "run:synthetic",
			Model:         "synthetic-model",
			PromptVersion: "extract-v2",
		},
		Status:     observation.StatusInferred,
		Confidence: confidence,
	})
	if err != nil {
		t.Fatalf("observation.NewObservation() error = %v", err)
	}
	return value
}

func canonicalEvidenceSpan(
	t *testing.T,
	input canonicalCitationInput,
) evidence.EvidenceSpan {
	t.Helper()
	quote := input.quote
	if quote == "" {
		quote = "Synthetic evidence."
	}
	section, err := evidence.NewSection(evidence.SectionInput{
		ID:    input.sectionID,
		Title: "Synthetic section",
		Path:  []string{"Synthetic section"},
		Role:  input.sectionRole,
		Text:  quote,
	})
	if err != nil {
		t.Fatalf("evidence.NewSection() error = %v", err)
	}
	document, err := evidence.NewDocumentVersion(evidence.DocumentVersionInput{
		Provider:           "synthetic",
		ProviderDocumentID: input.providerDocumentID,
		Title:              "Synthetic document",
		Locator:            input.locator,
		ProviderVersion:    "v1",
		ModifiedAt:         time.Date(2026, time.July, 20, 10, 0, 0, 0, time.UTC),
		RecordedAt:         time.Date(2026, time.July, 20, 11, 0, 0, 0, time.UTC),
		Sections:           []evidence.Section{section},
	})
	if err != nil {
		t.Fatalf("evidence.NewDocumentVersion() error = %v", err)
	}
	span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
		Document:    document,
		SectionID:   input.sectionID,
		StartOffset: 0,
		EndOffset:   len(quote),
		Quote:       quote,
		RecordedAt:  document.RecordedAt(),
	})
	if err != nil {
		t.Fatalf("evidence.NewEvidenceSpan() error = %v", err)
	}
	if input.id != "" && string(span.ID()) == input.id {
		t.Fatal("synthetic evidence fixture unexpectedly reused caller label")
	}
	return span
}
