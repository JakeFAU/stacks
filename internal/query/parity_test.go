package query

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
)

const (
	genericOwnerID    identity.EntityID = "entity:atlas-owner"
	genericReviewerID identity.EntityID = "entity:atlas-reviewer"

	genericStatePredicate     observation.Predicate = "atlas.responsibility/state"
	genericPairPredicate      observation.Predicate = "atlas.responsibility/pair"
	genericUnknownPredicate   observation.Predicate = "atlas.responsibility/unknown"
	genericAdmissionPredicate observation.Predicate = "atlas.responsibility/admission"
	genericGapPredicate       observation.Predicate = "atlas.responsibility/gap"
)

var (
	genericWindowStart      = time.Date(2034, time.January, 1, 0, 0, 0, 0, time.UTC)
	genericBoundary         = time.Date(2034, time.January, 15, 0, 0, 0, 0, time.UTC)
	genericConflictEnd      = time.Date(2034, time.January, 20, 0, 0, 0, 0, time.UTC)
	genericWindowEnd        = time.Date(2034, time.February, 1, 0, 0, 0, 0, time.UTC)
	genericHistoricalCutoff = time.Date(2034, time.January, 10, 12, 0, 0, 0, time.UTC)
)

func TestGenericTemporalQueryPreservesVerticalEvidenceParity(t *testing.T) {
	fixture := newGenericParityFixture(t)
	service := Service{
		Reader: fixture.reader,
		Limits: Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 32},
	}

	current, err := service.Query(t.Context(), fixture.currentRequest)
	if err != nil {
		t.Fatalf("current Query() error = %v", err)
	}
	historicalRequest := fixture.currentRequest
	historicalRequest.KnowledgeScope = fixture.historicalScope
	historical, err := service.Query(t.Context(), historicalRequest)
	if err != nil {
		t.Fatalf("as-of Query() error = %v", err)
	}
	if err := ValidateResult(current); err != nil {
		t.Fatalf("ValidateResult(current) error = %v", err)
	}
	if err := ValidateResult(historical); err != nil {
		t.Fatalf("ValidateResult(as-of) error = %v", err)
	}

	currentTrajectory, ok := current.Payload.Trajectory()
	if !ok {
		t.Fatal("current Payload.Trajectory() = false")
	}
	historicalTrajectory, ok := historical.Payload.Trajectory()
	if !ok {
		t.Fatal("as-of Payload.Trajectory() = false")
	}
	if currentTrajectory.Selection != fixture.window ||
		historicalTrajectory.Selection != fixture.window ||
		!reflect.DeepEqual(current.Selections, historical.Selections) {
		t.Fatal("valid-time selection changed across current and as-of knowledge")
	}

	wantCurrentChronology := []observation.ObservationID{
		"observation:generic/initial",
		"observation:generic/pair",
		"observation:generic/initial",
		"observation:generic/transfer",
	}
	if got := genericTransitionObservationIDs(currentTrajectory.Transitions); !slices.Equal(got, wantCurrentChronology) {
		t.Fatalf("current chronology = %v, want exact %v", got, wantCurrentChronology)
	}
	wantHistoricalChronology := []observation.ObservationID{
		"observation:generic/initial",
		"observation:generic/pair",
		"observation:generic/admission",
		"observation:generic/initial",
		"observation:generic/transfer",
	}
	if got := genericTransitionObservationIDs(historicalTrajectory.Transitions); !slices.Equal(got, wantHistoricalChronology) {
		t.Fatalf("as-of chronology = %v, want exact %v", got, wantHistoricalChronology)
	}

	initial := genericFactByObservationID(t, historicalTrajectory, "observation:generic/initial")
	if !reflect.DeepEqual(initial.Contributions, []Contribution{{
		ObservationID:             "observation:generic/initial",
		Status:                    observation.StatusObserved,
		ValidTime:                 genericDuring(t, genericWindowStart, genericBoundary),
		RecordedAt:                time.Date(2034, time.January, 2, 9, 20, 0, 0, time.UTC),
		Derivation:                observation.Derivation{Method: "synthetic-review", Version: "generic-parity-v1"},
		SubjectGroundingMentionID: "mention:atlas-owner",
	}}) {
		t.Fatalf("initial contribution = %#v, want exact reviewed identity and temporal provenance", initial.Contributions)
	}
	if !reflect.DeepEqual(initial.SupportingCitations, []Citation{fixture.supportCitation}) ||
		!reflect.DeepEqual(initial.ContradictingCitations, []Citation{fixture.counterCitation}) {
		t.Fatalf(
			"initial citations = support %#v counter %#v, want exact role-separated spans",
			initial.SupportingCitations,
			initial.ContradictingCitations,
		)
	}

	wantUnresolved := map[observation.Predicate]struct {
		reason temporal.UnresolvedReason
		ids    []observation.ObservationID
	}{
		genericStatePredicate: {
			reason: temporal.UnresolvedConflict,
			ids: []observation.ObservationID{
				"observation:generic/conflict",
				"observation:generic/initial",
				"observation:generic/transfer",
			},
		},
		genericUnknownPredicate: {
			reason: temporal.UnresolvedTemporalUncertainty,
			ids:    []observation.ObservationID{"observation:generic/unknown"},
		},
	}
	if got := genericUnresolvedShape(historicalTrajectory.Unresolved); !reflect.DeepEqual(got, wantUnresolved) {
		t.Fatalf("as-of unresolved = %#v, want exact conflicts and uncertainty %#v", got, wantUnresolved)
	}
	unknown := genericFactByObservationID(t, historicalTrajectory, "observation:generic/unknown")
	if unknown.Contributions[0].ValidTime.Kind() != observation.TemporalUnknown ||
		!reflect.DeepEqual(unknown.SupportingCitations, []Citation{fixture.supportCitation}) {
		t.Fatalf("unknown-time fact = %#v, want preserved uncertainty and exact support", unknown)
	}

	wantCurrentGaps := []Gap{
		{Kind: GapAuthorityExcluded, EntityID: genericOwnerID, Predicate: genericAdmissionPredicate},
		{Kind: GapUnresolvedMention, EntityID: genericReviewerID, Predicate: genericGapPredicate},
	}
	if !reflect.DeepEqual(current.Gaps, wantCurrentGaps) {
		t.Fatalf("current gaps = %#v, want exact %#v", current.Gaps, wantCurrentGaps)
	}
	wantHistoricalGaps := []Gap{{
		Kind: GapUnresolvedMention, EntityID: genericReviewerID, Predicate: genericGapPredicate,
	}}
	if !reflect.DeepEqual(historical.Gaps, wantHistoricalGaps) {
		t.Fatalf("as-of gaps = %#v, want exact %#v", historical.Gaps, wantHistoricalGaps)
	}
	if current.KnowledgeScope.Kind() != temporal.KnowledgeCurrent ||
		historical.KnowledgeScope.Kind() != temporal.KnowledgeAsOf {
		t.Fatalf("knowledge scopes = %q/%q, want current/as-of", current.KnowledgeScope.Kind(), historical.KnowledgeScope.Kind())
	}
	if got, ok := historical.KnowledgeScope.AsOf(); !ok || !got.Equal(genericHistoricalCutoff) {
		t.Fatalf("as-of cutoff = %v/%v, want %s", got, ok, genericHistoricalCutoff)
	}
}

type genericParityFixture struct {
	currentRequest  Request
	historicalScope temporal.KnowledgeScope
	window          temporal.TemporalSelection
	reader          *genericParityReader
	supportCitation Citation
	counterCitation Citation
}

func newGenericParityFixture(t *testing.T) genericParityFixture {
	t.Helper()
	window, err := temporal.Between("responsibility-window", genericWindowStart, genericWindowEnd)
	if err != nil {
		t.Fatal(err)
	}
	historicalScope, err := temporal.KnownAsOf(genericHistoricalCutoff)
	if err != nil {
		t.Fatal(err)
	}
	supportCitation := genericParityCitation(
		t,
		"support",
		"Generic Atlas record assigns a reviewed responsibility state.",
		"assigns a reviewed responsibility",
		observation.EvidenceSupporting,
	)
	counterCitation := genericParityCitation(
		t,
		"counter",
		"Generic Atlas record disputes the earlier responsibility state.",
		"disputes the earlier responsibility",
		observation.EvidenceContradicting,
	)

	ownerMention := genericMentionTerm(t, "mention:atlas-owner")
	reviewerMention := genericMentionTerm(t, "mention:atlas-reviewer")
	owner := genericEntityTerm(t, genericOwnerID)
	reviewer := genericEntityTerm(t, genericReviewerID)
	initial := genericReadObservation(t, genericObservationInput{
		id: "observation:generic/initial", subject: ownerMention, predicate: genericStatePredicate,
		object: genericTextTerm(t, "queued"), resolvedSubject: owner, resolvedObject: genericTextTerm(t, "queued"),
		subjectGrounding: "mention:atlas-owner",
		validTime:        genericDuring(t, genericWindowStart, genericBoundary),
		recordedAt:       time.Date(2034, time.January, 2, 9, 20, 0, 0, time.UTC),
		citations:        []Citation{counterCitation, supportCitation},
	})
	transfer := genericReadObservation(t, genericObservationInput{
		id: "observation:generic/transfer", subject: ownerMention, predicate: genericStatePredicate,
		object: genericTextTerm(t, "active"), resolvedSubject: owner, resolvedObject: genericTextTerm(t, "active"),
		subjectGrounding: "mention:atlas-owner",
		validTime:        genericDuring(t, genericBoundary, genericWindowEnd),
		recordedAt:       time.Date(2034, time.January, 3, 9, 20, 0, 0, time.UTC),
		citations:        []Citation{supportCitation},
	})
	conflict := genericReadObservation(t, genericObservationInput{
		id: "observation:generic/conflict", subject: ownerMention, predicate: genericStatePredicate,
		object: genericTextTerm(t, "paused"), resolvedSubject: owner, resolvedObject: genericTextTerm(t, "paused"),
		subjectGrounding: "mention:atlas-owner",
		validTime:        genericDuring(t, genericBoundary, genericConflictEnd),
		recordedAt:       time.Date(2034, time.January, 3, 9, 21, 0, 0, time.UTC),
		citations:        []Citation{counterCitation, supportCitation},
	})
	pair := genericReadObservation(t, genericObservationInput{
		id: "observation:generic/pair", subject: ownerMention, predicate: genericPairPredicate,
		object: reviewerMention, resolvedSubject: owner, resolvedObject: reviewer,
		subjectGrounding: "mention:atlas-owner", objectGrounding: "mention:atlas-reviewer",
		validTime:  genericDuring(t, genericWindowStart, genericWindowEnd),
		recordedAt: time.Date(2034, time.January, 2, 9, 22, 0, 0, time.UTC),
		citations:  []Citation{supportCitation},
	})
	unknown := genericReadObservation(t, genericObservationInput{
		id: "observation:generic/unknown", subject: ownerMention, predicate: genericUnknownPredicate,
		object: reviewerMention, resolvedSubject: owner, resolvedObject: reviewer,
		subjectGrounding: "mention:atlas-owner", objectGrounding: "mention:atlas-reviewer",
		validTime:  observation.UnknownTime(),
		recordedAt: time.Date(2034, time.January, 2, 9, 23, 0, 0, time.UTC),
		citations:  []Citation{supportCitation},
	})
	admissionScoped := genericReadObservation(t, genericObservationInput{
		id: "observation:generic/admission", subject: ownerMention, predicate: genericAdmissionPredicate,
		object: reviewerMention, resolvedSubject: owner, resolvedObject: reviewer,
		subjectGrounding: "mention:atlas-owner", objectGrounding: "mention:atlas-reviewer",
		validTime:  genericDuring(t, genericWindowStart, genericWindowEnd),
		recordedAt: time.Date(2034, time.January, 2, 9, 24, 0, 0, time.UTC),
		citations:  []Citation{supportCitation},
	})

	common := []ReadObservation{unknown, conflict, transfer, initial, pair}
	current := ReadSnapshot{
		Entities: []EntityAuthority{
			{EntityID: genericOwnerID, Known: true},
			{EntityID: genericReviewerID, Known: true},
		},
		Observations: append([]ReadObservation{}, common...),
		Coverage: []Coverage{
			{
				Reason: CoverageUnresolvedMention, EntityID: genericReviewerID,
				Predicate: genericGapPredicate, ObservationID: "observation:generic/gap",
				ValidTime: observation.UnknownTime(),
			},
			{
				Reason: CoverageAuthorityExcluded, EntityID: genericOwnerID,
				Predicate: genericAdmissionPredicate, ObservationID: admissionScoped.Observation.ID(),
				ValidTime: admissionScoped.Observation.ValidTime(),
			},
		},
	}
	historical := ReadSnapshot{
		Entities:     append([]EntityAuthority{}, current.Entities...),
		Observations: append(append([]ReadObservation{}, common...), admissionScoped),
		Coverage:     append([]Coverage{}, current.Coverage[:1]...),
	}
	request := Request{
		Intent: temporal.IntentTrajectory,
		EntityIDs: []identity.EntityID{
			genericReviewerID,
			genericOwnerID,
		},
		EntityMatch: EntityMatchAny,
		Predicates: []observation.Predicate{
			genericUnknownPredicate,
			genericStatePredicate,
			genericPairPredicate,
			genericAdmissionPredicate,
			genericGapPredicate,
		},
		Selections:     []temporal.TemporalSelection{window},
		KnowledgeScope: temporal.CurrentKnowledge(),
		Limit:          32,
	}
	return genericParityFixture{
		currentRequest: request, historicalScope: historicalScope, window: window,
		reader: &genericParityReader{
			current: current, historical: historical, cutoff: genericHistoricalCutoff,
		},
		supportCitation: supportCitation,
		counterCitation: counterCitation,
	}
}

type genericParityReader struct {
	current    ReadSnapshot
	historical ReadSnapshot
	cutoff     time.Time
}

func (reader *genericParityReader) Read(_ context.Context, selection ReadSelection) (ReadSnapshot, error) {
	if selection.KnowledgeScope.Kind() == temporal.KnowledgeCurrent {
		return cloneReadSnapshot(reader.current), nil
	}
	cutoff, ok := selection.KnowledgeScope.AsOf()
	if !ok || !cutoff.Equal(reader.cutoff) {
		return ReadSnapshot{}, context.Canceled
	}
	return cloneReadSnapshot(reader.historical), nil
}

type genericObservationInput struct {
	id               observation.ObservationID
	subject          observation.Term
	predicate        observation.Predicate
	object           observation.Term
	resolvedSubject  observation.Term
	resolvedObject   observation.Term
	subjectGrounding string
	objectGrounding  string
	validTime        observation.TemporalExtent
	recordedAt       time.Time
	citations        []Citation
}

func genericReadObservation(t *testing.T, input genericObservationInput) ReadObservation {
	t.Helper()
	links := make([]observation.EvidenceLink, len(input.citations))
	for index, citation := range input.citations {
		links[index] = observation.EvidenceLink{EvidenceID: citation.EvidenceID, Role: citation.Role}
	}
	value, err := observation.NewObservation(observation.ObservationInput{
		ID: input.id,
		Statement: observation.Statement{
			Subject: input.subject, Predicate: input.predicate, Object: input.object,
		},
		ValidTime: input.validTime, RecordedAt: input.recordedAt, Evidence: links,
		Derivation: observation.Derivation{Method: "synthetic-review", Version: "generic-parity-v1"},
		Status:     observation.StatusObserved,
	})
	if err != nil {
		t.Fatalf("observation.NewObservation(%q) error = %v", input.id, err)
	}
	return ReadObservation{
		Observation: value, Subject: input.resolvedSubject, Object: input.resolvedObject,
		SubjectGroundingMentionID: input.subjectGrounding,
		ObjectGroundingMentionID:  input.objectGrounding,
		Evidence:                  cloneCitations(input.citations),
	}
}

func genericParityCitation(
	t *testing.T,
	id string,
	text string,
	quote string,
	role observation.EvidenceRole,
) Citation {
	t.Helper()
	section, err := evidence.NewSection(evidence.SectionInput{
		ID: "section:generic/" + id, Title: "Generic Atlas record",
		Path: []string{"Generic Atlas", "Responsibility"}, Order: 0,
		Role: "synthetic-record", Text: text,
	})
	if err != nil {
		t.Fatal(err)
	}
	recordedAt := time.Date(2034, time.January, 2, 9, 0, 0, 0, time.UTC)
	document, err := evidence.NewDocumentVersion(evidence.DocumentVersionInput{
		Provider: "synthetic", ProviderDocumentID: "document:generic/" + id,
		Title: "Generic Atlas fixture", Locator: "synthetic://generic-atlas/" + id,
		ProviderVersion: "v1", ModifiedAt: recordedAt, RecordedAt: recordedAt,
		Sections: []evidence.Section{section},
	})
	if err != nil {
		t.Fatal(err)
	}
	start := len(text) - len(quote) - 7
	if start < 0 || text[start:start+len(quote)] != quote {
		for start = 0; start+len(quote) <= len(text); start++ {
			if text[start:start+len(quote)] == quote {
				break
			}
		}
	}
	span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
		Document: document, SectionID: section.ID(), StartOffset: start,
		EndOffset: start + len(quote), Quote: quote, RecordedAt: recordedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return Citation{
		EvidenceID: span.ID(), Role: role,
		SourceDocumentID: document.ProviderDocumentID(), DocumentVersionID: document.Digest().String(),
		SectionID: section.ID(), SectionTitle: section.Title(), SectionPath: section.Path(),
		SectionOrder: section.Order(), SectionRole: section.Role(),
		StartOffset: span.StartOffset(), EndOffset: span.EndOffset(),
		Locator: span.Locator(), Text: span.Text(),
	}
}

func genericEntityTerm(t *testing.T, id identity.EntityID) observation.Term {
	t.Helper()
	value, err := observation.NewEntityTerm(string(id), "")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func genericMentionTerm(t *testing.T, id identity.MentionID) observation.Term {
	t.Helper()
	value, err := observation.NewMentionTerm(string(id))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func genericTextTerm(t *testing.T, text string) observation.Term {
	t.Helper()
	value, err := observation.NewTextTerm(text)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func genericDuring(t *testing.T, start, end time.Time) observation.TemporalExtent {
	t.Helper()
	value, err := observation.During(start, end)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func genericTransitionObservationIDs(values []Transition) []observation.ObservationID {
	var ids []observation.ObservationID
	for _, value := range values {
		if value.Before != nil && len(value.Before.Contributions) > 0 {
			ids = append(ids, value.Before.Contributions[0].ObservationID)
		}
		if value.After != nil && len(value.After.Contributions) > 0 {
			ids = append(ids, value.After.Contributions[0].ObservationID)
		}
	}
	return ids
}

func genericFactByObservationID(
	t *testing.T,
	value TrajectoryResult,
	observationID observation.ObservationID,
) Fact {
	t.Helper()
	for _, transition := range value.Transitions {
		for _, fact := range []*Fact{transition.Before, transition.After} {
			if fact != nil && len(fact.Contributions) > 0 &&
				fact.Contributions[0].ObservationID == observationID {
				return *fact
			}
		}
	}
	for _, unresolved := range value.Unresolved {
		for _, candidate := range unresolved.Candidates {
			if len(candidate.Contributions) > 0 &&
				candidate.Contributions[0].ObservationID == observationID {
				return candidate
			}
		}
	}
	t.Fatalf("observation %q is absent from trajectory", observationID)
	return Fact{}
}

func genericUnresolvedShape(values []UnresolvedItem) map[observation.Predicate]struct {
	reason temporal.UnresolvedReason
	ids    []observation.ObservationID
} {
	result := make(map[observation.Predicate]struct {
		reason temporal.UnresolvedReason
		ids    []observation.ObservationID
	}, len(values))
	for _, value := range values {
		ids := make([]observation.ObservationID, 0, len(value.Candidates))
		for _, candidate := range value.Candidates {
			for _, contribution := range candidate.Contributions {
				ids = append(ids, contribution.ObservationID)
			}
		}
		slices.Sort(ids)
		result[value.Key.Predicate] = struct {
			reason temporal.UnresolvedReason
			ids    []observation.ObservationID
		}{reason: value.Reason, ids: ids}
	}
	return result
}
