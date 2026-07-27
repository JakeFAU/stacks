package query

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
)

func TestProjectTrendRejectsMissingObservationContributionWithoutLeakingIdentifiers(t *testing.T) {
	fixture := newTrendFixture(t)
	key, err := temporal.NewStateKey(fixture.entity("entity-a"), "work.location")
	if err != nil {
		t.Fatalf("temporal.NewStateKey() error = %v", err)
	}
	const privateObservationID = "private-missing-observation"
	comparison := temporal.Comparison{
		Before: fixture.request.Selections[0],
		After:  fixture.request.Selections[1],
		BeforeFacts: []temporal.Fact{{
			Key:                   key,
			Value:                 fixture.text("remote"),
			ObservationIDs:        []observation.ObservationID{privateObservationID},
			SupportingEvidenceIDs: []evidence.EvidenceID{"evidence-support"},
		}},
	}

	_, err = projectTrend(comparison, projectionIndex{})
	assertBoundedProjectionError(t, err, privateObservationID)
}

func TestTrendQueryRejectsSnapshotProjectionIntegrityViolations(t *testing.T) {
	tests := []struct {
		name    string
		private string
		mutate  func(*testing.T, trendFixture, *ReadSnapshot)
	}{
		{
			name:    "observation link names absent evidence",
			private: "private-absent-evidence",
			mutate: func(t *testing.T, fixture trendFixture, snapshot *ReadSnapshot) {
				read := fixture.readObservation("private-observation", fixture.entity("entity-a"), fixture.text("remote"), fixture.instant(2024, time.January, 15), observation.StatusObserved, nil, fixture.citation("private-absent-evidence", observation.EvidenceSupporting))
				read.Evidence = nil
				snapshot.Observations = []ReadObservation{read}
			},
		},
		{
			name:    "stable evidence metadata conflicts",
			private: "private-conflicting-title",
			mutate: func(t *testing.T, fixture trendFixture, snapshot *ReadSnapshot) {
				citation := fixture.citation("shared-evidence", observation.EvidenceSupporting)
				first := fixture.readObservation("observation-a", fixture.entity("entity-a"), fixture.text("remote"), fixture.instant(2024, time.January, 15), observation.StatusObserved, nil, citation)
				citation.SectionTitle = "private-conflicting-title"
				second := fixture.readObservationWithPredicate("observation-b", "work.mode", fixture.entity("entity-a"), fixture.text("hybrid"), fixture.instant(2024, time.January, 16), observation.StatusObserved, nil, citation)
				snapshot.Observations = []ReadObservation{first, second}
			},
		},
		{
			name:    "citation role differs from observation link",
			private: "private-role-evidence",
			mutate: func(t *testing.T, fixture trendFixture, snapshot *ReadSnapshot) {
				read := fixture.readObservation("observation-role", fixture.entity("entity-a"), fixture.text("remote"), fixture.instant(2024, time.January, 15), observation.StatusObserved, nil, fixture.citation("private-role-evidence", observation.EvidenceSupporting))
				read.Evidence[0].Role = observation.EvidenceContradicting
				snapshot.Observations = []ReadObservation{read}
			},
		},
		{
			name:    "resolved direct term differs",
			private: "private-wrong-entity",
			mutate: func(t *testing.T, fixture trendFixture, snapshot *ReadSnapshot) {
				read := fixture.readObservation("observation-direct", fixture.entity("entity-a"), fixture.text("remote"), fixture.instant(2024, time.January, 15), observation.StatusObserved, nil, fixture.citation("evidence-direct", observation.EvidenceSupporting))
				read.Subject = fixture.entity("private-wrong-entity")
				snapshot.Observations = []ReadObservation{read}
			},
		},
		{
			name:    "resolved mention lacks matching grounding",
			private: "private-wrong-mention",
			mutate: func(t *testing.T, fixture trendFixture, snapshot *ReadSnapshot) {
				mention, err := observation.NewMentionTerm("source-mention")
				if err != nil {
					t.Fatalf("observation.NewMentionTerm() error = %v", err)
				}
				read := fixture.readObservation("observation-mention", mention, fixture.text("remote"), fixture.instant(2024, time.January, 15), observation.StatusObserved, nil, fixture.citation("evidence-mention", observation.EvidenceSupporting))
				read.Subject = fixture.entity("entity-a")
				read.SubjectGroundingMentionID = "private-wrong-mention"
				snapshot.Observations = []ReadObservation{read}
			},
		},
		{
			name:    "stable observation carries different canonical payload",
			private: "private-second-payload",
			mutate: func(t *testing.T, fixture trendFixture, snapshot *ReadSnapshot) {
				first := fixture.readObservation("shared-observation", fixture.entity("entity-a"), fixture.text("remote"), fixture.instant(2024, time.January, 15), observation.StatusObserved, nil, fixture.citation("evidence-first", observation.EvidenceSupporting))
				second := fixture.readObservation("shared-observation", fixture.entity("entity-a"), fixture.text("private-second-payload"), fixture.instant(2024, time.January, 15), observation.StatusObserved, nil, fixture.citation("evidence-second", observation.EvidenceSupporting))
				snapshot.Observations = []ReadObservation{first, second}
			},
		},
		{
			name:    "coverage reason is unknown",
			private: "private-unknown-coverage",
			mutate: func(t *testing.T, fixture trendFixture, snapshot *ReadSnapshot) {
				snapshot.Coverage = []Coverage{{Reason: CoverageReason("private-unknown-coverage"), EntityID: "entity-a"}}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrendFixture(t)
			snapshot := fixture.snapshot()
			test.mutate(t, fixture, &snapshot)
			_, err := (Service{Reader: &recordingTrendReader{snapshot: snapshot}, Limits: validLimits()}).Query(context.Background(), fixture.request)
			assertBoundedProjectionError(t, err, test.private)
		})
	}
}

func TestTrendQueryAcceptsDirectTermsAndMatchingMentionGrounding(t *testing.T) {
	fixture := newTrendFixture(t)
	mention, err := observation.NewMentionTerm("mention-a")
	if err != nil {
		t.Fatalf("observation.NewMentionTerm() error = %v", err)
	}
	direct := fixture.readObservation("direct", fixture.entity("entity-a"), fixture.text("remote"), fixture.instant(2024, time.January, 15), observation.StatusObserved, nil, fixture.citation("evidence-direct", observation.EvidenceSupporting))
	grounded := fixture.readObservationWithPredicate("grounded", "work.owner", mention, fixture.text("owner"), fixture.instant(2024, time.January, 16), observation.StatusObserved, nil, fixture.citation("evidence-grounded", observation.EvidenceSupporting))
	grounded.Subject = fixture.entity("entity-a")
	grounded.SubjectGroundingMentionID = "mention-a"

	request := fixture.request
	request.Predicates = nil
	result, err := (Service{Reader: &recordingTrendReader{snapshot: fixture.snapshot(direct, grounded)}, Limits: validLimits()}).Query(context.Background(), request)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	trend, _ := result.Payload.Trend()
	if len(trend.Before.Facts) != 2 {
		t.Fatalf("before facts = %d, want 2", len(trend.Before.Facts))
	}
	var foundGrounded bool
	for _, fact := range trend.Before.Facts {
		for _, contribution := range fact.Contributions {
			if contribution.ObservationID == "grounded" {
				foundGrounded = contribution.SubjectGroundingMentionID == "mention-a"
			}
		}
	}
	if !foundGrounded {
		t.Fatal("grounded mention contribution was not preserved")
	}
}

func TestTrendQueryPreservesDirectEntityGroundingForSubjectAndObject(t *testing.T) {
	fixture := newTrendFixture(t)
	canonicalSubject := fixture.entity("entity-a")
	canonicalObject := fixture.entity("entity-b")

	subject := fixture.readObservationWithPredicate(
		"direct-grounded-subject",
		"work.owner",
		mustGroundedEntityTerm(t, "entity-a", "mention-subject"),
		fixture.text("owner"),
		fixture.instant(2024, time.January, 15),
		observation.StatusObserved,
		nil,
		fixture.citation("evidence-direct-subject", observation.EvidenceSupporting),
	)
	subject.Subject = canonicalSubject
	subject.SubjectGroundingMentionID = "mention-subject"

	object := fixture.readObservationWithPredicate(
		"direct-grounded-object",
		"work.assignment",
		canonicalSubject,
		mustGroundedEntityTerm(t, "entity-b", "mention-object"),
		fixture.instant(2024, time.January, 16),
		observation.StatusObserved,
		nil,
		fixture.citation("evidence-direct-object", observation.EvidenceSupporting),
	)
	object.Object = canonicalObject
	object.ObjectGroundingMentionID = "mention-object"

	request := fixture.request
	request.Predicates = nil
	result, err := (Service{
		Reader: &recordingTrendReader{snapshot: fixture.snapshot(subject, object)},
		Limits: validLimits(),
	}).Query(context.Background(), request)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	trend, ok := result.Payload.Trend()
	if !ok {
		t.Fatal("Trend() ok = false")
	}

	found := make(map[observation.ObservationID]bool)
	for _, fact := range trend.Before.Facts {
		for _, contribution := range fact.Contributions {
			switch contribution.ObservationID {
			case "direct-grounded-subject":
				_, groundingMentionID, isEntity := fact.Key.Subject.Entity()
				found[contribution.ObservationID] =
					contribution.SubjectGroundingMentionID == "mention-subject" &&
						isEntity &&
						groundingMentionID == ""
			case "direct-grounded-object":
				_, groundingMentionID, isEntity := fact.Value.Entity()
				found[contribution.ObservationID] =
					contribution.ObjectGroundingMentionID == "mention-object" &&
						isEntity &&
						groundingMentionID == ""
			}
		}
	}
	for _, observationID := range []observation.ObservationID{"direct-grounded-subject", "direct-grounded-object"} {
		if !found[observationID] {
			t.Fatalf("direct grounding for %q was not preserved outside canonical state", observationID)
		}
	}
}

func TestTrendQueryRejectsDirectEntityGroundingDisagreement(t *testing.T) {
	tests := []struct {
		name   string
		source func(*testing.T, trendFixture) ReadObservation
	}{
		{
			name: "subject missing",
			source: func(t *testing.T, fixture trendFixture) ReadObservation {
				read := fixture.readObservation(
					"direct-subject-missing",
					mustGroundedEntityTerm(t, "entity-a", "private-subject-mention"),
					fixture.text("owner"),
					fixture.instant(2024, time.January, 15),
					observation.StatusObserved,
					nil,
					fixture.citation("evidence-direct-subject-missing", observation.EvidenceSupporting),
				)
				read.Subject = fixture.entity("entity-a")
				return read
			},
		},
		{
			name: "subject differs",
			source: func(t *testing.T, fixture trendFixture) ReadObservation {
				read := fixture.readObservation(
					"direct-subject-differs",
					mustGroundedEntityTerm(t, "entity-a", "private-subject-mention"),
					fixture.text("owner"),
					fixture.instant(2024, time.January, 15),
					observation.StatusObserved,
					nil,
					fixture.citation("evidence-direct-subject-differs", observation.EvidenceSupporting),
				)
				read.Subject = fixture.entity("entity-a")
				read.SubjectGroundingMentionID = "different-subject-mention"
				return read
			},
		},
		{
			name: "object missing",
			source: func(t *testing.T, fixture trendFixture) ReadObservation {
				read := fixture.readObservation(
					"direct-object-missing",
					fixture.entity("entity-a"),
					mustGroundedEntityTerm(t, "entity-b", "private-object-mention"),
					fixture.instant(2024, time.January, 15),
					observation.StatusObserved,
					nil,
					fixture.citation("evidence-direct-object-missing", observation.EvidenceSupporting),
				)
				read.Object = fixture.entity("entity-b")
				return read
			},
		},
		{
			name: "object differs",
			source: func(t *testing.T, fixture trendFixture) ReadObservation {
				read := fixture.readObservation(
					"direct-object-differs",
					fixture.entity("entity-a"),
					mustGroundedEntityTerm(t, "entity-b", "private-object-mention"),
					fixture.instant(2024, time.January, 15),
					observation.StatusObserved,
					nil,
					fixture.citation("evidence-direct-object-differs", observation.EvidenceSupporting),
				)
				read.Object = fixture.entity("entity-b")
				read.ObjectGroundingMentionID = "different-object-mention"
				return read
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrendFixture(t)
			_, err := (Service{
				Reader: &recordingTrendReader{snapshot: fixture.snapshot(test.source(t, fixture))},
				Limits: validLimits(),
			}).Query(context.Background(), fixture.request)
			assertBoundedProjectionError(t, err, "private-subject-mention", "private-object-mention")
		})
	}
}

func assertBoundedProjectionError(t *testing.T, err error, privateValues ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("projection error = nil")
	}
	if !strings.Contains(err.Error(), "project temporal snapshot") {
		t.Fatalf("projection error = %q, want bounded operation name", err)
	}
	for _, privateValue := range privateValues {
		if strings.Contains(err.Error(), privateValue) {
			t.Fatalf("projection error leaked %q: %v", privateValue, err)
		}
	}
}
