package query

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/adapters/postgres/coremigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
	"github.com/jackc/pgx/v5"
)

func TestPostgresRepositoryPostgresCancellation(t *testing.T) {
	isolated := postgrestest.NewDatabase(t)
	manifest, err := coremigrations.Manifest()
	if err != nil {
		t.Fatalf("coremigrations.Manifest() error = %v", err)
	}
	applicationConfig, err := pgx.ParseConfig(isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("parse application database URL: %v", err)
	}
	if _, err := (migration.Migrator{
		DatabaseURL:     isolated.AdminURL(),
		ApplicationRole: applicationConfig.User,
		Manifests:       []migration.Manifest{manifest},
	}).Apply(context.Background()); err != nil {
		t.Fatalf("apply core migrations: %v", err)
	}
	database, err := postgres.Open(
		context.Background(),
		isolated.ApplicationURL(),
	)
	if err != nil {
		t.Fatalf("postgres.Open() error = %v", err)
	}
	t.Cleanup(database.Close)
	repository := PostgresRepository{Database: database}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = repository.Read(ctx, postgresTestReadSelection(t))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() error = %v, want context.Canceled", err)
	}
}

func TestPostgresRepositoryExecutesAllTemporalIntentsOverSyntheticAtlas(t *testing.T) {
	fixture := newAllIntentPostgresFixture(t)
	service := Service{
		Reader: PostgresRepository{Database: fixture.database},
		Limits: Limits{MaxEntities: 16, MaxPredicates: 32, MaxChronology: 1000},
	}
	queryPoint := func(at time.Time, scope temporal.KnowledgeScope) Result {
		t.Helper()
		selection, err := temporal.At("point", at)
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Query(t.Context(), Request{
			Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{fixture.ownerID},
			EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{fixture.responsibilityPredicate},
			Selections: []temporal.TemporalSelection{selection}, KnowledgeScope: scope,
		})
		if err != nil {
			t.Fatalf("point Query() error = %v", err)
		}
		return result
	}

	before := queryPoint(fixture.boundary.Add(-time.Second), temporal.CurrentKnowledge())
	beforePoint, ok := before.Payload.Point()
	wantInitial := allIntentFact(
		t,
		fixture,
		fixture.responsibilityPredicate,
		"Alex",
		"observation:project-atlas/responsibility-initial",
		mustAllIntentDuring(t, fixture.windowStart, fixture.boundary),
		time.Date(2026, time.January, 2, 9, 10, 0, 0, time.UTC),
		false,
	)
	if !ok || !reflect.DeepEqual(beforePoint.Facts, []Fact{wantInitial}) ||
		len(beforePoint.Unresolved) != 0 {
		t.Fatalf("before point = %#v, want exact cited initial responsibility", beforePoint)
	}
	assertAllIntentGaps(t, before.Gaps, []Gap{{
		Kind: GapValidTimeExcluded, EntityID: fixture.ownerID,
		Predicate: fixture.responsibilityPredicate, SelectionLabel: "point",
	}})

	wantTransfer := allIntentFact(
		t,
		fixture,
		fixture.responsibilityPredicate,
		"Blair",
		"observation:project-atlas/responsibility-transfer",
		mustAllIntentDuring(t, fixture.boundary, fixture.windowEnd),
		time.Date(2026, time.January, 30, 9, 0, 0, 0, time.UTC),
		false,
	)
	wantConflict := allIntentFact(
		t,
		fixture,
		fixture.responsibilityPredicate,
		"Casey",
		"observation:project-atlas/responsibility-conflict",
		mustAllIntentDuring(t, fixture.boundary, fixture.conflictEnd),
		time.Date(2026, time.January, 30, 9, 1, 0, 0, time.UTC),
		true,
	)
	at := queryPoint(fixture.boundary, temporal.CurrentKnowledge())
	atPoint, ok := at.Payload.Point()
	wantConflictItem := UnresolvedItem{
		Key:        wantTransfer.Key,
		Reason:     temporal.UnresolvedConflict,
		Candidates: []Fact{wantTransfer, wantConflict},
	}
	if !ok || len(atPoint.Facts) != 0 ||
		!reflect.DeepEqual(atPoint.Unresolved, []UnresolvedItem{wantConflictItem}) {
		t.Fatalf("boundary point = %#v, want exact cited conflict %#v", atPoint, wantConflictItem)
	}
	assertAllIntentGaps(t, at.Gaps, []Gap{{
		Kind: GapValidTimeExcluded, EntityID: fixture.ownerID,
		Predicate: fixture.responsibilityPredicate, SelectionLabel: "point",
	}})

	after := queryPoint(fixture.conflictEnd, temporal.CurrentKnowledge())
	afterPoint, ok := after.Payload.Point()
	if !ok || !reflect.DeepEqual(afterPoint.Facts, []Fact{wantTransfer}) ||
		len(afterPoint.Unresolved) != 0 {
		t.Fatalf("after point = %#v, want exact cited transferred responsibility", afterPoint)
	}
	assertAllIntentGaps(t, after.Gaps, []Gap{{
		Kind: GapValidTimeExcluded, EntityID: fixture.ownerID,
		Predicate: fixture.responsibilityPredicate, SelectionLabel: "point",
	}})

	historicalScope, err := temporal.KnownAsOf(fixture.historicalCutoff)
	if err != nil {
		t.Fatal(err)
	}
	historical := queryPoint(fixture.conflictEnd, historicalScope)
	historicalPoint, ok := historical.Payload.Point()
	if !ok || len(historicalPoint.Facts) != 0 || len(historicalPoint.Unresolved) != 0 {
		t.Fatalf("historical point = %#v, want later authority hidden", historicalPoint)
	}
	assertAllIntentGaps(t, historical.Gaps, []Gap{{
		Kind: GapValidTimeExcluded, EntityID: fixture.ownerID,
		Predicate: fixture.responsibilityPredicate, SelectionLabel: "point",
	}})
	if !reflect.DeepEqual(after.Selections, historical.Selections) {
		t.Fatal("valid-time selections differ across recorded-time scope")
	}

	authorityCurrent := queryPointForPredicate(
		t,
		service,
		fixture.ownerID,
		fixture.authorityPredicate,
		fixture.conflictEnd,
		temporal.CurrentKnowledge(),
	)
	authorityHistorical := queryPointForPredicate(
		t,
		service,
		fixture.ownerID,
		fixture.authorityPredicate,
		fixture.conflictEnd,
		historicalScope,
	)
	currentAuthorityPoint, ok := authorityCurrent.Payload.Point()
	if !ok || len(currentAuthorityPoint.Facts) != 0 {
		t.Fatalf("current authority point = %#v, want retired observation hidden", currentAuthorityPoint)
	}
	historicalAuthorityPoint, ok := authorityHistorical.Payload.Point()
	wantHistoricalAuthority := allIntentFact(
		t,
		fixture,
		fixture.authorityPredicate,
		"visible historically",
		"observation:project-atlas/authority-scope",
		mustAllIntentDuring(t, fixture.windowStart, fixture.windowEnd),
		time.Date(2026, time.January, 2, 9, 15, 0, 0, time.UTC),
		false,
	)
	if !ok ||
		!reflect.DeepEqual(historicalAuthorityPoint.Facts, []Fact{wantHistoricalAuthority}) ||
		len(historicalAuthorityPoint.Unresolved) != 0 {
		t.Fatalf("historical authority point = %#v, want exact cutoff-effective admitted fact", historicalAuthorityPoint)
	}
	if !reflect.DeepEqual(authorityCurrent.Selections, authorityHistorical.Selections) {
		t.Fatal("authority comparison changed valid-time selection")
	}
	assertAllIntentGaps(t, authorityCurrent.Gaps, []Gap{{
		Kind: GapAuthorityExcluded, EntityID: fixture.ownerID,
		Predicate: fixture.authorityPredicate,
	}})
	assertAllIntentGaps(t, authorityHistorical.Gaps, []Gap{})

	window, err := temporal.Between("between", fixture.windowStart, fixture.windowEnd)
	if err != nil {
		t.Fatal(err)
	}
	trajectory, err := service.Query(t.Context(), Request{
		Intent: temporal.IntentTrajectory, EntityIDs: []identity.EntityID{fixture.ownerID},
		EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{
			fixture.responsibilityPredicate, fixture.uncertaintyPredicate,
		},
		Selections:     []temporal.TemporalSelection{window},
		KnowledgeScope: temporal.CurrentKnowledge(), Limit: 10,
	})
	if err != nil {
		t.Fatalf("trajectory Query() error = %v", err)
	}
	trajectoryPayload, ok := trajectory.Payload.Trajectory()
	wantUncertainty := allIntentFact(
		t,
		fixture,
		fixture.uncertaintyPredicate,
		"uncertain",
		"observation:project-atlas/uncertainty",
		observation.UnknownTime(),
		time.Date(2026, time.January, 2, 9, 11, 0, 0, time.UTC),
		false,
	)
	wantTransitions := []Transition{
		{
			Kind: temporal.ChangeAdded, Key: wantInitial.Key,
			ValidTime: mustAllIntentAt(t, fixture.windowStart),
			After:     factPointer(wantInitial), Unresolved: []UnresolvedItem{},
		},
		{
			Kind: temporal.ChangeRemoved, Key: wantInitial.Key,
			ValidTime:  mustAllIntentAt(t, fixture.boundary),
			Before:     factPointer(wantInitial),
			Unresolved: []UnresolvedItem{wantConflictItem},
		},
		{
			Kind: temporal.ChangeAdded, Key: wantInitial.Key,
			ValidTime:  mustAllIntentAt(t, fixture.conflictEnd),
			After:      factPointer(wantTransfer),
			Unresolved: []UnresolvedItem{wantConflictItem},
		},
	}
	wantTrajectoryUnresolved := []UnresolvedItem{
		{
			Key: wantInitial.Key, Reason: temporal.UnresolvedConflict,
			Candidates: []Fact{wantInitial, wantTransfer, wantConflict},
		},
		{
			Key: wantUncertainty.Key, Reason: temporal.UnresolvedTemporalUncertainty,
			Candidates: []Fact{wantUncertainty},
		},
	}
	if !ok || trajectoryPayload.Selection != window {
		t.Fatalf("trajectory selection = %#v, want %#v", trajectoryPayload.Selection, window)
	}
	if len(trajectoryPayload.Transitions) != len(wantTransitions) {
		t.Fatalf("trajectory transition count = %d, want %d", len(trajectoryPayload.Transitions), len(wantTransitions))
	}
	for index, wantTransition := range wantTransitions {
		if !reflect.DeepEqual(trajectoryPayload.Transitions[index], wantTransition) {
			t.Fatalf(
				"trajectory transition[%d] = %#v, want exact %#v",
				index,
				trajectoryPayload.Transitions[index],
				wantTransition,
			)
		}
	}
	if !reflect.DeepEqual(trajectoryPayload.Unresolved, wantTrajectoryUnresolved) {
		t.Fatalf(
			"trajectory unresolved = %#v, want exact %#v",
			trajectoryPayload.Unresolved,
			wantTrajectoryUnresolved,
		)
	}
	assertAllIntentGaps(t, trajectory.Gaps, []Gap{})

	causal, err := service.Query(t.Context(), Request{
		Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{fixture.ownerID},
		EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{CausalPredicate},
		Selections:     []temporal.TemporalSelection{window},
		KnowledgeScope: temporal.CurrentKnowledge(), Limit: 10,
	})
	if err != nil {
		t.Fatalf("causal Query() error = %v", err)
	}
	causalPayload, ok := causal.Payload.Causal()
	ownerTerm := mustAllIntentEntityTerm(t, fixture.ownerID)
	handoffTerm := mustAllIntentTextTerm(t, "handoff")
	wantCausalLinks := []CausalLink{
		{
			Cause: ownerTerm, Effect: handoffTerm,
			Contributions: []Contribution{allIntentContribution(
				"observation:project-atlas/cause-one",
				mustAllIntentAt(t, fixture.windowStart.Add(5*24*time.Hour)),
				time.Date(2026, time.January, 2, 9, 12, 0, 0, time.UTC),
			)},
			SupportingCitations:    []Citation{fixture.supportCitation},
			ContradictingCitations: []Citation{fixture.counterCitation},
		},
		{
			Cause: handoffTerm, Effect: ownerTerm,
			Contributions: []Contribution{allIntentContribution(
				"observation:project-atlas/cause-two",
				mustAllIntentAt(t, fixture.windowStart.Add(10*24*time.Hour)),
				time.Date(2026, time.January, 2, 9, 13, 0, 0, time.UTC),
			)},
			SupportingCitations:    []Citation{fixture.supportCitation},
			ContradictingCitations: []Citation{},
		},
	}
	if !ok || causalPayload.Selection != window ||
		!reflect.DeepEqual(causalPayload.Links, wantCausalLinks) {
		t.Fatalf("causal = %#v, want exact ordered links %#v", causalPayload, wantCausalLinks)
	}
	assertAllIntentGaps(t, causal.Gaps, []Gap{})

	chronologyOnly, err := service.Query(t.Context(), Request{
		Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{fixture.chronologyOnlyID},
		EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{CausalPredicate},
		Selections:     []temporal.TemporalSelection{window},
		KnowledgeScope: temporal.CurrentKnowledge(), Limit: 10,
	})
	if err != nil {
		t.Fatalf("chronology-only causal Query() error = %v", err)
	}
	chronologyPayload, ok := chronologyOnly.Payload.Causal()
	if !ok || chronologyPayload.Selection != window ||
		!reflect.DeepEqual(chronologyPayload.Links, []CausalLink{}) {
		t.Fatalf("chronology-only causal result = %#v", chronologyOnly)
	}
	assertAllIntentGaps(t, chronologyOnly.Gaps, []Gap{{
		Kind: GapNoCausalEvidence, Predicate: CausalPredicate, SelectionLabel: "between",
	}})

	for _, test := range []struct {
		name    string
		request Request
	}{
		{
			name: "trajectory",
			request: Request{
				Intent: temporal.IntentTrajectory, EntityIDs: []identity.EntityID{fixture.ownerID},
				EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{fixture.responsibilityPredicate},
				Selections:     []temporal.TemporalSelection{window},
				KnowledgeScope: temporal.CurrentKnowledge(), Limit: 1,
			},
		},
		{
			name: "causal",
			request: Request{
				Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{fixture.ownerID},
				EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{CausalPredicate},
				Selections:     []temporal.TemporalSelection{window},
				KnowledgeScope: temporal.CurrentKnowledge(), Limit: 1,
			},
		},
	} {
		t.Run(test.name+" overflow", func(t *testing.T) {
			result, err := service.Query(t.Context(), test.request)
			if !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("Query() error = %v, want ErrLimitExceeded", err)
			}
			if !reflect.DeepEqual(result, Result{}) {
				t.Fatalf("overflow returned partial result: %#v", result)
			}
		})
	}
}

func TestPostgresRepositoryPreservesGenericTemporalEvidenceParity(t *testing.T) {
	base := newAllIntentPostgresFixture(t)
	fixture := seedGenericParityPostgresFixture(t, base.database)
	postgresService := Service{
		Reader: PostgresRepository{Database: base.database},
		Limits: Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 32},
	}
	memoryService := Service{
		Reader: fixture.reader,
		Limits: Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 32},
	}

	for _, test := range []struct {
		name  string
		scope temporal.KnowledgeScope
	}{
		{name: "current", scope: temporal.CurrentKnowledge()},
		{name: "as-of", scope: fixture.historicalScope},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := fixture.currentRequest
			request.KnowledgeScope = test.scope
			want, err := memoryService.Query(t.Context(), request)
			if err != nil {
				t.Fatalf("in-memory Query() error = %v", err)
			}
			got, err := postgresService.Query(t.Context(), request)
			if err != nil {
				t.Fatalf("PostgresRepository Query() error = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("PostgreSQL parity mismatch\n got: %#v\nwant: %#v", got, want)
			}
			if test.name == "current" {
				var authorityEntities []identity.EntityID
				for _, gap := range got.Gaps {
					if gap.Kind == GapAuthorityExcluded &&
						gap.Predicate == genericAdmissionPredicate {
						authorityEntities = append(authorityEntities, gap.EntityID)
					}
				}
				if !slices.Equal(authorityEntities, []identity.EntityID{
					genericOwnerID,
					genericReviewerID,
				}) {
					t.Fatalf(
						"current admission authority gaps = %v, want exact owner and reviewer endpoints",
						authorityEntities,
					)
				}
			}
		})
	}

	allRequest := fixture.currentRequest
	allRequest.EntityMatch = EntityMatchAll
	allRequest.Predicates = []observation.Predicate{genericPairPredicate}
	allRequest.KnowledgeScope = fixture.historicalScope
	all, err := postgresService.Query(t.Context(), allRequest)
	if err != nil {
		t.Fatalf("EntityMatchAll Query() error = %v", err)
	}
	trajectory, ok := all.Payload.Trajectory()
	if !ok || len(trajectory.Transitions) != 1 ||
		trajectory.Transitions[0].After == nil ||
		trajectory.Transitions[0].After.Contributions[0].ObservationID != "observation:generic/pair" {
		t.Fatalf("EntityMatchAll trajectory = %#v, want the reviewed generic pair", trajectory)
	}
}

func seedGenericParityPostgresFixture(t *testing.T, database *postgres.Database) genericParityFixture {
	t.Helper()
	recordedBase := time.Date(2034, time.January, 2, 9, 0, 0, 0, time.UTC)
	type storedEvidence struct {
		document evidence.DocumentVersion
		section  evidence.Section
		span     evidence.EvidenceSpan
		ref      postgres.DocumentVersionRef
		citation Citation
	}
	newEvidence := func(id, text, quote string, role observation.EvidenceRole) storedEvidence {
		t.Helper()
		section, err := evidence.NewSection(evidence.SectionInput{
			ID: "section:generic/" + id, Title: "Generic Atlas record",
			Path: []string{"Generic Atlas", "Responsibility"}, Order: 0,
			Role: "synthetic-record", Text: text,
		})
		if err != nil {
			t.Fatal(err)
		}
		document, err := evidence.NewDocumentVersion(evidence.DocumentVersionInput{
			Provider: "synthetic", ProviderDocumentID: "document:generic/" + id,
			Title: "Generic Atlas fixture", Locator: "synthetic://generic-atlas/" + id,
			ProviderVersion: "v1", ModifiedAt: recordedBase, RecordedAt: recordedBase,
			Sections: []evidence.Section{section},
		})
		if err != nil {
			t.Fatal(err)
		}
		ref, err := database.PutDocumentVersion(t.Context(), document)
		if err != nil {
			t.Fatal(err)
		}
		start := strings.Index(text, quote)
		if start < 0 {
			t.Fatalf("quote %q is absent from generic evidence", quote)
		}
		span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
			Document: document, SectionID: section.ID(), StartOffset: start,
			EndOffset: start + len(quote), Quote: quote, RecordedAt: recordedBase.Add(time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		return storedEvidence{
			document: document, section: section, span: span, ref: ref.Ref,
			citation: Citation{
				EvidenceID: span.ID(), Role: role,
				SourceDocumentID: ref.Ref.SourceDocumentID, DocumentVersionID: ref.Ref.VersionID,
				SectionID: section.ID(), SectionTitle: section.Title(), SectionPath: section.Path(),
				SectionOrder: section.Order(), SectionRole: section.Role(),
				StartOffset: span.StartOffset(), EndOffset: span.EndOffset(),
				Locator: span.Locator(), Text: span.Text(),
			},
		}
	}
	support := newEvidence(
		"support",
		"Generic Atlas record assigns a reviewed responsibility state.",
		"assigns a reviewed responsibility",
		observation.EvidenceSupporting,
	)
	counter := newEvidence(
		"counter",
		"Generic Atlas record disputes the earlier responsibility state.",
		"disputes the earlier responsibility",
		observation.EvidenceContradicting,
	)

	mustEntity := func(id identity.EntityID, displayName string) identity.Entity {
		t.Helper()
		value, err := identity.NewEntity(identity.EntityInput{
			ID: id, Kind: identity.KindPerson, DisplayName: displayName,
			RecordedAt: recordedBase.Add(2 * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	mustMention := func(id identity.MentionID, surface string) identity.MentionRecord {
		t.Helper()
		value, err := identity.NewMention(identity.MentionInput{
			ID: id, EvidenceID: support.span.ID(), DerivationRunID: "run:generic/identity",
			Surface: surface, NormalizedName: identity.NormalizeName(surface),
			Role: "participant", RecordedAt: recordedBase.Add(3 * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	mustProposal := func(id identity.ProposalID, mentionID identity.MentionID) identity.ResolutionProposal {
		t.Helper()
		value, err := identity.NewResolutionProposal(identity.ResolutionProposalInput{
			ID: id, MentionID: mentionID, ReasonCode: "reviewed_identity",
			EvidenceIDs: []evidence.EvidenceID{support.span.ID()},
			RecordedAt:  recordedBase.Add(4 * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	mustDecision := func(id identity.DecisionID, proposalID identity.ProposalID, entityID identity.EntityID) identity.ResolutionDecision {
		t.Helper()
		value, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
			ID: id, ProposalID: proposalID, Outcome: identity.DecisionAccepted,
			EntityID: entityID, Authority: identity.AuthorityReviewer,
			ReasonCode: "reviewed_identity", RecordedAt: recordedBase.Add(5 * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	mustAdmission := func(
		id string,
		targetKind admission.TargetKind,
		targetID string,
		outcome admission.Outcome,
		recordedAt time.Time,
		supersedes string,
	) admission.Decision {
		t.Helper()
		value, err := admission.NewDecision(admission.DecisionInput{
			ID: id, TargetKind: targetKind, TargetID: targetID, Outcome: outcome,
			ReasonCode: "reviewed_authority", Authority: admission.AuthorityReviewer,
			RecordedAt: recordedAt, SupersedesID: supersedes,
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	ownerEntity := mustEntity(genericOwnerID, "Generic Atlas Owner")
	reviewerEntity := mustEntity(genericReviewerID, "Generic Atlas Reviewer")
	ownerMention := mustMention("mention:atlas-owner", "Generic Atlas Owner")
	reviewerMention := mustMention("mention:atlas-reviewer", "Generic Atlas Reviewer")
	gapMention := mustMention("mention:atlas-gap", "Generic Atlas Participant")
	ownerProposal := mustProposal("proposal:atlas-owner", ownerMention.ID())
	reviewerProposal := mustProposal("proposal:atlas-reviewer", reviewerMention.ID())
	ownerDecision := mustDecision("decision:atlas-owner", ownerProposal.ID(), ownerEntity.ID())
	reviewerDecision := mustDecision("decision:atlas-reviewer", reviewerProposal.ID(), reviewerEntity.ID())

	ownerMentionTerm := genericMentionTerm(t, ownerMention.ID())
	reviewerMentionTerm := genericMentionTerm(t, reviewerMention.ID())
	gapMentionTerm := genericMentionTerm(t, gapMention.ID())
	ownerTerm := genericEntityTerm(t, genericOwnerID)
	reviewerTerm := genericEntityTerm(t, genericReviewerID)
	inputs := []genericObservationInput{
		{
			id: "observation:generic/initial", subject: ownerMentionTerm, predicate: genericStatePredicate,
			object: genericTextTerm(t, "queued"), resolvedSubject: ownerTerm, resolvedObject: genericTextTerm(t, "queued"),
			subjectGrounding: string(ownerMention.ID()),
			validTime:        genericDuring(t, genericWindowStart, genericBoundary),
			recordedAt:       time.Date(2034, time.January, 2, 9, 20, 0, 0, time.UTC),
			citations:        []Citation{counter.citation, support.citation},
		},
		{
			id: "observation:generic/transfer", subject: ownerMentionTerm, predicate: genericStatePredicate,
			object: genericTextTerm(t, "active"), resolvedSubject: ownerTerm, resolvedObject: genericTextTerm(t, "active"),
			subjectGrounding: string(ownerMention.ID()),
			validTime:        genericDuring(t, genericBoundary, genericWindowEnd),
			recordedAt:       time.Date(2034, time.January, 3, 9, 20, 0, 0, time.UTC),
			citations:        []Citation{support.citation},
		},
		{
			id: "observation:generic/conflict", subject: ownerMentionTerm, predicate: genericStatePredicate,
			object: genericTextTerm(t, "paused"), resolvedSubject: ownerTerm, resolvedObject: genericTextTerm(t, "paused"),
			subjectGrounding: string(ownerMention.ID()),
			validTime:        genericDuring(t, genericBoundary, genericConflictEnd),
			recordedAt:       time.Date(2034, time.January, 3, 9, 21, 0, 0, time.UTC),
			citations:        []Citation{counter.citation, support.citation},
		},
		{
			id: "observation:generic/pair", subject: ownerMentionTerm, predicate: genericPairPredicate,
			object: reviewerMentionTerm, resolvedSubject: ownerTerm, resolvedObject: reviewerTerm,
			subjectGrounding: string(ownerMention.ID()), objectGrounding: string(reviewerMention.ID()),
			validTime:  genericDuring(t, genericWindowStart, genericWindowEnd),
			recordedAt: time.Date(2034, time.January, 2, 9, 22, 0, 0, time.UTC),
			citations:  []Citation{support.citation},
		},
		{
			id: "observation:generic/unknown", subject: ownerMentionTerm, predicate: genericUnknownPredicate,
			object: reviewerMentionTerm, resolvedSubject: ownerTerm, resolvedObject: reviewerTerm,
			subjectGrounding: string(ownerMention.ID()), objectGrounding: string(reviewerMention.ID()),
			validTime:  observation.UnknownTime(),
			recordedAt: time.Date(2034, time.January, 2, 9, 23, 0, 0, time.UTC),
			citations:  []Citation{support.citation},
		},
		{
			id: "observation:generic/admission", subject: ownerMentionTerm, predicate: genericAdmissionPredicate,
			object: reviewerMentionTerm, resolvedSubject: ownerTerm, resolvedObject: reviewerTerm,
			subjectGrounding: string(ownerMention.ID()), objectGrounding: string(reviewerMention.ID()),
			validTime:  genericDuring(t, genericWindowStart, genericWindowEnd),
			recordedAt: time.Date(2034, time.January, 2, 9, 24, 0, 0, time.UTC),
			citations:  []Citation{support.citation},
		},
		{
			id: "observation:generic/gap", subject: ownerMentionTerm, predicate: genericGapPredicate,
			object: gapMentionTerm, resolvedSubject: ownerTerm, resolvedObject: gapMentionTerm,
			subjectGrounding: string(ownerMention.ID()),
			validTime:        observation.UnknownTime(),
			recordedAt:       time.Date(2034, time.January, 2, 9, 25, 0, 0, time.UTC),
			citations:        []Citation{support.citation},
		},
	}
	readObservations := make([]ReadObservation, len(inputs))
	observations := make([]observation.Observation, len(inputs))
	for index, input := range inputs {
		read := genericReadObservation(t, input)
		readObservations[index] = read
		observations[index] = read.Observation
	}

	identityAdmissions := []admission.Decision{
		mustAdmission("admission:mention:atlas-owner", admission.TargetMention, string(ownerMention.ID()), admission.Admitted, recordedBase.Add(6*time.Minute), ""),
		mustAdmission("admission:mention:atlas-reviewer", admission.TargetMention, string(reviewerMention.ID()), admission.Admitted, recordedBase.Add(7*time.Minute), ""),
		mustAdmission("admission:mention:atlas-gap", admission.TargetMention, string(gapMention.ID()), admission.Admitted, recordedBase.Add(8*time.Minute), ""),
		mustAdmission("admission:decision:atlas-owner", admission.TargetIdentityDecision, string(ownerDecision.ID()), admission.Admitted, recordedBase.Add(9*time.Minute), ""),
		mustAdmission("admission:decision:atlas-reviewer", admission.TargetIdentityDecision, string(reviewerDecision.ID()), admission.Admitted, recordedBase.Add(10*time.Minute), ""),
	}
	observationAdmissions := make([]admission.Decision, len(observations))
	for index, value := range observations {
		observationAdmissions[index] = mustAdmission(
			"admission:"+string(value.ID()), admission.TargetObservation, string(value.ID()),
			admission.Admitted, value.RecordedAt().Add(time.Minute), "",
		)
	}
	retiredAdmission := mustAdmission(
		"admission:observation:generic/admission#retired",
		admission.TargetObservation,
		"observation:generic/admission",
		admission.Retired,
		time.Date(2034, time.January, 25, 9, 0, 0, 0, time.UTC),
		"admission:observation:generic/admission",
	)

	if err := database.InTransaction(t.Context(), func(transaction *postgres.Transaction) error {
		for _, item := range []storedEvidence{support, counter} {
			if err := transaction.SetCurrentDocumentVersion(t.Context(), item.ref.SourceDocumentID, item.ref.VersionID); err != nil {
				return err
			}
			if _, err := transaction.PutEvidenceSpan(t.Context(), item.span); err != nil {
				return err
			}
		}
		for _, entity := range []identity.Entity{ownerEntity, reviewerEntity} {
			if _, err := transaction.PutEntity(t.Context(), entity); err != nil {
				return err
			}
		}
		for _, mention := range []identity.MentionRecord{ownerMention, reviewerMention, gapMention} {
			if _, err := transaction.PutMention(t.Context(), mention); err != nil {
				return err
			}
		}
		for _, proposal := range []identity.ResolutionProposal{ownerProposal, reviewerProposal} {
			if _, err := transaction.PutResolutionProposal(t.Context(), proposal); err != nil {
				return err
			}
		}
		for _, decision := range []identity.ResolutionDecision{ownerDecision, reviewerDecision} {
			if err := transaction.AppendResolutionDecision(t.Context(), decision, nil); err != nil {
				return err
			}
		}
		for _, decision := range identityAdmissions {
			if err := transaction.AppendAdmissionDecision(t.Context(), decision); err != nil {
				return err
			}
		}
		for _, value := range observations {
			if _, err := transaction.PutObservation(t.Context(), value); err != nil {
				return err
			}
		}
		for _, decision := range observationAdmissions {
			if err := transaction.AppendAdmissionDecision(t.Context(), decision); err != nil {
				return err
			}
		}
		return transaction.AppendAdmissionDecision(t.Context(), retiredAdmission)
	}); err != nil {
		t.Fatalf("seed generic parity PostgreSQL fixture: %v", err)
	}

	window, err := temporal.Between("responsibility-window", genericWindowStart, genericWindowEnd)
	if err != nil {
		t.Fatal(err)
	}
	historicalScope, err := temporal.KnownAsOf(genericHistoricalCutoff)
	if err != nil {
		t.Fatal(err)
	}
	currentRead := []ReadObservation{
		readObservations[4], readObservations[2], readObservations[1],
		readObservations[0], readObservations[3],
	}
	current := ReadSnapshot{
		Entities: []EntityAuthority{
			{EntityID: genericOwnerID, Known: true},
			{EntityID: genericReviewerID, Known: true},
		},
		Observations: currentRead,
		Coverage: []Coverage{
			{
				Reason: CoverageUnresolvedMention, EntityID: genericOwnerID,
				Predicate: genericGapPredicate, ObservationID: observations[6].ID(),
				ValidTime: observation.UnknownTime(),
			},
			{
				Reason: CoverageAuthorityExcluded, EntityID: genericOwnerID,
				Predicate: genericAdmissionPredicate, ObservationID: observations[5].ID(),
				ValidTime: observations[5].ValidTime(),
			},
			{
				Reason: CoverageAuthorityExcluded, EntityID: genericReviewerID,
				Predicate: genericAdmissionPredicate, ObservationID: observations[5].ID(),
				ValidTime: observations[5].ValidTime(),
			},
		},
	}
	historical := ReadSnapshot{
		Entities:     append([]EntityAuthority{}, current.Entities...),
		Observations: append(append([]ReadObservation{}, currentRead...), readObservations[5]),
		Coverage: []Coverage{{
			Reason: CoverageUnresolvedMention, EntityID: genericOwnerID,
			Predicate: genericGapPredicate, ObservationID: observations[6].ID(),
			ValidTime: observation.UnknownTime(),
		}},
	}
	return genericParityFixture{
		currentRequest: Request{
			Intent:      temporal.IntentTrajectory,
			EntityIDs:   []identity.EntityID{genericReviewerID, genericOwnerID},
			EntityMatch: EntityMatchAny,
			Predicates: []observation.Predicate{
				genericUnknownPredicate, genericStatePredicate, genericPairPredicate,
				genericAdmissionPredicate, genericGapPredicate,
			},
			Selections:     []temporal.TemporalSelection{window},
			KnowledgeScope: temporal.CurrentKnowledge(), Limit: 32,
		},
		historicalScope: historicalScope, window: window,
		reader: &genericParityReader{
			current: current, historical: historical, cutoff: genericHistoricalCutoff,
		},
		supportCitation: support.citation, counterCitation: counter.citation,
	}
}

func queryPointForPredicate(
	t *testing.T,
	service Service,
	entityID identity.EntityID,
	predicate observation.Predicate,
	at time.Time,
	scope temporal.KnowledgeScope,
) Result {
	t.Helper()
	selection, err := temporal.At("point", at)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Query(t.Context(), Request{
		Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{entityID},
		EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{predicate},
		Selections: []temporal.TemporalSelection{selection}, KnowledgeScope: scope,
	})
	if err != nil {
		t.Fatalf("point Query() error = %v", err)
	}
	return result
}

func allIntentFact(
	t *testing.T,
	fixture allIntentPostgresFixture,
	predicate observation.Predicate,
	value string,
	observationID observation.ObservationID,
	validTime observation.TemporalExtent,
	recordedAt time.Time,
	counter bool,
) Fact {
	t.Helper()
	subject, err := observation.NewEntityTerm(string(fixture.ownerID), "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := temporal.NewStateKey(subject, predicate)
	if err != nil {
		t.Fatal(err)
	}
	object, err := observation.NewTextTerm(value)
	if err != nil {
		t.Fatal(err)
	}
	contradicting := []Citation{}
	if counter {
		contradicting = []Citation{fixture.counterCitation}
	}
	return Fact{
		Key: key, Value: object,
		Contributions: []Contribution{{
			ObservationID: observationID,
			Status:        observation.StatusObserved,
			ValidTime:     validTime,
			RecordedAt:    recordedAt,
			Derivation: observation.Derivation{
				Method: "synthetic", Version: "all-intents-v1",
			},
		}},
		SupportingCitations:    []Citation{fixture.supportCitation},
		ContradictingCitations: contradicting,
	}
}

func mustAllIntentDuring(t *testing.T, start, end time.Time) observation.TemporalExtent {
	t.Helper()
	value, err := observation.During(start, end)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustAllIntentAt(t *testing.T, at time.Time) observation.TemporalExtent {
	t.Helper()
	value, err := observation.AtTime(at)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustAllIntentTextTerm(t *testing.T, value string) observation.Term {
	t.Helper()
	term, err := observation.NewTextTerm(value)
	if err != nil {
		t.Fatal(err)
	}
	return term
}

func mustAllIntentEntityTerm(t *testing.T, entityID identity.EntityID) observation.Term {
	t.Helper()
	term, err := observation.NewEntityTerm(string(entityID), "")
	if err != nil {
		t.Fatal(err)
	}
	return term
}

func allIntentContribution(
	observationID observation.ObservationID,
	validTime observation.TemporalExtent,
	recordedAt time.Time,
) Contribution {
	return Contribution{
		ObservationID: observationID,
		Status:        observation.StatusObserved,
		ValidTime:     validTime,
		RecordedAt:    recordedAt,
		Derivation: observation.Derivation{
			Method: "synthetic", Version: "all-intents-v1",
		},
	}
}

func factPointer(value Fact) *Fact {
	return &value
}

func assertAllIntentGaps(t *testing.T, got, want []Gap) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gaps = %#v, want exact %#v", got, want)
	}
}

type allIntentPostgresFixture struct {
	database                *postgres.Database
	ownerID                 identity.EntityID
	chronologyOnlyID        identity.EntityID
	responsibilityPredicate observation.Predicate
	uncertaintyPredicate    observation.Predicate
	authorityPredicate      observation.Predicate
	windowStart             time.Time
	boundary                time.Time
	conflictEnd             time.Time
	windowEnd               time.Time
	historicalCutoff        time.Time
	supportCitation         Citation
	counterCitation         Citation
}

func newAllIntentPostgresFixture(t *testing.T) allIntentPostgresFixture {
	t.Helper()
	isolated := postgrestest.NewDatabase(t)
	manifest, err := coremigrations.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	applicationConfig, err := pgx.ParseConfig(isolated.ApplicationURL())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migration.Migrator{
		DatabaseURL: isolated.AdminURL(), ApplicationRole: applicationConfig.User,
		Manifests: []migration.Manifest{manifest},
	}).Apply(t.Context()); err != nil {
		t.Fatalf("apply core migrations: %v", err)
	}
	database, err := postgres.Open(t.Context(), isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("open application database: %v", err)
	}
	t.Cleanup(database.Close)

	fixture := allIntentPostgresFixture{
		database: database,
		ownerID:  "entity:project-atlas/owner", chronologyOnlyID: "entity:project-atlas/chronology-only",
		responsibilityPredicate: "project.atlas/responsibility",
		uncertaintyPredicate:    "project.atlas/uncertainty",
		authorityPredicate:      "project.atlas/authority-scope",
		windowStart:             time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		boundary:                time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC),
		conflictEnd:             time.Date(2026, time.January, 20, 0, 0, 0, 0, time.UTC),
		windowEnd:               time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC),
		historicalCutoff:        time.Date(2026, time.January, 20, 12, 0, 0, 0, time.UTC),
	}
	fixture.seed(t)
	return fixture
}

func (fixture *allIntentPostgresFixture) seed(t *testing.T) {
	t.Helper()
	recordedBase := time.Date(2026, time.January, 2, 9, 0, 0, 0, time.UTC)
	supportText := "Project Atlas synthetic support records responsibility and causality."
	counterText := "Project Atlas synthetic counterevidence challenges the causal link."
	supportSection, err := evidence.NewSection(evidence.SectionInput{
		ID: "section:project-atlas/all-intents-support", Title: "Synthetic support",
		Path: []string{"Project Atlas"}, Order: 0, Role: "synthetic", Text: supportText,
	})
	if err != nil {
		t.Fatal(err)
	}
	counterSection, err := evidence.NewSection(evidence.SectionInput{
		ID: "section:project-atlas/all-intents-counter", Title: "Synthetic counterevidence",
		Path: []string{"Project Atlas"}, Order: 1, Role: "synthetic", Text: counterText,
	})
	if err != nil {
		t.Fatal(err)
	}
	document, err := evidence.NewDocumentVersion(evidence.DocumentVersionInput{
		Provider: "synthetic", ProviderDocumentID: "project-atlas-all-intents",
		Title:   "Synthetic Project Atlas all-intent fixture",
		Locator: "synthetic://project-atlas/all-intents", ProviderVersion: "v1",
		ModifiedAt: recordedBase, RecordedAt: recordedBase, SourceTime: &recordedBase,
		Sections: []evidence.Section{supportSection, counterSection},
	})
	if err != nil {
		t.Fatal(err)
	}
	documentRef, err := fixture.database.PutDocumentVersion(t.Context(), document)
	if err != nil {
		t.Fatal(err)
	}
	newSpan := func(section evidence.Section, text, quote string, recordedAt time.Time) evidence.EvidenceSpan {
		t.Helper()
		start := strings.Index(text, quote)
		if start < 0 {
			t.Fatalf("quote %q not found", quote)
		}
		span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
			Document: document, SectionID: section.ID(), StartOffset: start,
			EndOffset: start + len(quote), Quote: quote, RecordedAt: recordedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		return span
	}
	supportSpan := newSpan(supportSection, supportText, "synthetic support", recordedBase.Add(time.Minute))
	counterSpan := newSpan(counterSection, counterText, "synthetic counterevidence", recordedBase.Add(2*time.Minute))
	fixture.supportCitation = Citation{
		EvidenceID:        supportSpan.ID(),
		Role:              observation.EvidenceSupporting,
		SourceDocumentID:  documentRef.Ref.SourceDocumentID,
		DocumentVersionID: documentRef.Ref.VersionID,
		SectionID:         supportSection.ID(),
		SectionTitle:      supportSection.Title(),
		SectionPath:       supportSection.Path(),
		SectionOrder:      supportSection.Order(),
		SectionRole:       supportSection.Role(),
		StartOffset:       supportSpan.StartOffset(),
		EndOffset:         supportSpan.EndOffset(),
		Locator:           document.Locator(),
		Text:              supportSpan.Text(),
	}
	fixture.counterCitation = Citation{
		EvidenceID:        counterSpan.ID(),
		Role:              observation.EvidenceContradicting,
		SourceDocumentID:  documentRef.Ref.SourceDocumentID,
		DocumentVersionID: documentRef.Ref.VersionID,
		SectionID:         counterSection.ID(),
		SectionTitle:      counterSection.Title(),
		SectionPath:       counterSection.Path(),
		SectionOrder:      counterSection.Order(),
		SectionRole:       counterSection.Role(),
		StartOffset:       counterSpan.StartOffset(),
		EndOffset:         counterSpan.EndOffset(),
		Locator:           document.Locator(),
		Text:              counterSpan.Text(),
	}

	newEntity := func(id identity.EntityID, name string) identity.Entity {
		t.Helper()
		value, err := identity.NewEntity(identity.EntityInput{
			ID: id, Kind: identity.KindPerson, DisplayName: name, RecordedAt: recordedBase.Add(3 * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	owner := newEntity(fixture.ownerID, "Synthetic Atlas Owner")
	chronologyOnly := newEntity(fixture.chronologyOnlyID, "Synthetic Atlas Chronology")
	ownerTerm, err := observation.NewEntityTerm(string(fixture.ownerID), "")
	if err != nil {
		t.Fatal(err)
	}
	chronologyTerm, err := observation.NewEntityTerm(string(fixture.chronologyOnlyID), "")
	if err != nil {
		t.Fatal(err)
	}
	textTerm := func(value string) observation.Term {
		t.Helper()
		term, err := observation.NewTextTerm(value)
		if err != nil {
			t.Fatal(err)
		}
		return term
	}
	during := func(start, end time.Time) observation.TemporalExtent {
		t.Helper()
		value, err := observation.During(start, end)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	at := func(value time.Time) observation.TemporalExtent {
		t.Helper()
		result, err := observation.AtTime(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	newObservation := func(
		id string,
		subject observation.Term,
		predicate observation.Predicate,
		object observation.Term,
		validTime observation.TemporalExtent,
		recordedAt time.Time,
		counter bool,
		status observation.EpistemicStatus,
	) observation.Observation {
		t.Helper()
		links := []observation.EvidenceLink{{
			EvidenceID: supportSpan.ID(), Role: observation.EvidenceSupporting,
		}}
		if counter {
			links = append(links, observation.EvidenceLink{
				EvidenceID: counterSpan.ID(), Role: observation.EvidenceContradicting,
			})
		}
		value, err := observation.NewObservation(observation.ObservationInput{
			ID:        observation.ObservationID(id),
			Statement: observation.Statement{Subject: subject, Predicate: predicate, Object: object},
			ValidTime: validTime, RecordedAt: recordedAt, Evidence: links,
			Derivation: observation.Derivation{Method: "synthetic", Version: "all-intents-v1"},
			Status:     status,
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	lateRecorded := time.Date(2026, time.January, 30, 9, 0, 0, 0, time.UTC)
	observations := []observation.Observation{
		newObservation("observation:project-atlas/responsibility-initial", ownerTerm,
			fixture.responsibilityPredicate, textTerm("Alex"), during(fixture.windowStart, fixture.boundary),
			recordedBase.Add(10*time.Minute), false, observation.StatusObserved),
		newObservation("observation:project-atlas/responsibility-transfer", ownerTerm,
			fixture.responsibilityPredicate, textTerm("Blair"), during(fixture.boundary, fixture.windowEnd),
			lateRecorded, false, observation.StatusObserved),
		newObservation("observation:project-atlas/responsibility-conflict", ownerTerm,
			fixture.responsibilityPredicate, textTerm("Casey"), during(fixture.boundary, fixture.conflictEnd),
			lateRecorded.Add(time.Minute), true, observation.StatusObserved),
		newObservation("observation:project-atlas/uncertainty", ownerTerm,
			fixture.uncertaintyPredicate, textTerm("uncertain"), observation.UnknownTime(),
			recordedBase.Add(11*time.Minute), false, observation.StatusObserved),
		newObservation("observation:project-atlas/authority-scope", ownerTerm,
			fixture.authorityPredicate, textTerm("visible historically"),
			during(fixture.windowStart, fixture.windowEnd),
			recordedBase.Add(15*time.Minute), false, observation.StatusObserved),
		newObservation("observation:project-atlas/cause-one", ownerTerm,
			CausalPredicate, textTerm("handoff"), at(fixture.windowStart.Add(5*24*time.Hour)),
			recordedBase.Add(12*time.Minute), true, observation.StatusObserved),
		newObservation("observation:project-atlas/cause-two", textTerm("handoff"),
			CausalPredicate, ownerTerm, at(fixture.windowStart.Add(10*24*time.Hour)),
			recordedBase.Add(13*time.Minute), false, observation.StatusObserved),
		newObservation("observation:project-atlas/chronology-only", chronologyTerm,
			fixture.responsibilityPredicate, textTerm("dated change"), at(fixture.boundary),
			recordedBase.Add(14*time.Minute), false, observation.StatusObserved),
	}
	admissions := make([]admission.Decision, len(observations))
	for index, value := range observations {
		recordedAt := value.RecordedAt().Add(time.Minute)
		decision, err := admission.NewDecision(admission.DecisionInput{
			ID:         "admission:" + string(value.ID()),
			TargetKind: admission.TargetObservation, TargetID: string(value.ID()),
			Outcome: admission.Admitted, ReasonCode: "synthetic_acceptance",
			Authority: admission.AuthorityReviewer, RecordedAt: recordedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		admissions[index] = decision
	}
	authoritySuccessor, err := admission.NewDecision(admission.DecisionInput{
		ID:         "admission:observation:project-atlas/authority-scope#retired",
		TargetKind: admission.TargetObservation,
		TargetID:   "observation:project-atlas/authority-scope",
		Outcome:    admission.Retired,
		ReasonCode: "synthetic_successor",
		Authority:  admission.AuthorityReviewer,
		RecordedAt: lateRecorded.Add(10 * time.Minute),
		SupersedesID: "admission:" +
			"observation:project-atlas/authority-scope",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := fixture.database.InTransaction(t.Context(), func(transaction *postgres.Transaction) error {
		if err := transaction.SetCurrentDocumentVersion(
			t.Context(), documentRef.Ref.SourceDocumentID, documentRef.Ref.VersionID,
		); err != nil {
			return err
		}
		for _, span := range []evidence.EvidenceSpan{supportSpan, counterSpan} {
			if _, err := transaction.PutEvidenceSpan(t.Context(), span); err != nil {
				return err
			}
		}
		for _, entity := range []identity.Entity{owner, chronologyOnly} {
			if _, err := transaction.PutEntity(t.Context(), entity); err != nil {
				return err
			}
		}
		for _, value := range observations {
			if _, err := transaction.PutObservation(t.Context(), value); err != nil {
				return err
			}
		}
		for _, decision := range admissions {
			if err := transaction.AppendAdmissionDecision(t.Context(), decision); err != nil {
				return err
			}
		}
		if err := transaction.AppendAdmissionDecision(t.Context(), authoritySuccessor); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed synthetic all-intent PostgreSQL fixture: %v", err)
	}
}
