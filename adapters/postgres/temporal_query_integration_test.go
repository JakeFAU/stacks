package postgres

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

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

const temporalQueryPostgresTimeout = 30 * time.Second

const (
	atlasInitialOwnerID identity.EntityID = "entity:project-atlas/alex-initial"
	atlasCurrentOwnerID identity.EntityID = "entity:project-atlas/alex-current"
	atlasCollaboratorID identity.EntityID = "entity:project-atlas/blair"
	atlasObserverID     identity.EntityID = "entity:project-atlas/casey"

	atlasOwnerPredicate         observation.Predicate = "project.atlas/owner"
	atlasStatusPredicate        observation.Predicate = "project.atlas/status"
	atlasCollaborationPredicate observation.Predicate = "project.atlas/collaborates"
	atlasLateSourcePredicate    observation.Predicate = "project.atlas/late-source"
	atlasConcurrentPredicate    observation.Predicate = "project.atlas/concurrent"

	atlasSupportingSectionText    = "Alex leads Project Atlas with Blair."
	atlasSupportingQuote          = "leads Project Atlas"
	atlasSupportingStartOffset    = 5
	atlasSupportingEndOffset      = 24
	atlasContradictingSectionText = "A reviewer recorded counterevidence about Atlas ownership."
	atlasContradictingQuote       = "counterevidence about Atlas"
	atlasContradictingStartOffset = 20
	atlasContradictingEndOffset   = 47
	atlasLateSourceSectionText    = "A later source disputes Project Atlas collaboration."
	atlasLateSourceQuote          = "disputes Project Atlas"
	atlasLateSourceStartOffset    = 15
	atlasLateSourceEndOffset      = 37
)

var (
	atlasSourceTime                 = time.Date(2026, time.January, 2, 8, 55, 0, 0, time.UTC)
	atlasDocumentRecordedAt         = time.Date(2026, time.January, 2, 9, 0, 0, 0, time.UTC)
	atlasEntityRecordedAt           = time.Date(2026, time.January, 2, 9, 5, 0, 0, time.UTC)
	atlasOwnerMentionRecordedAt     = time.Date(2026, time.January, 2, 9, 10, 0, 0, time.UTC)
	atlasCollaboratorMentionAt      = time.Date(2026, time.January, 2, 9, 11, 0, 0, time.UTC)
	atlasOwnerProposalRecordedAt    = time.Date(2026, time.January, 2, 9, 15, 0, 0, time.UTC)
	atlasCollaboratorProposalAt     = time.Date(2026, time.January, 2, 9, 16, 0, 0, time.UTC)
	atlasOwnerInitialDecisionAt     = time.Date(2026, time.January, 2, 9, 20, 0, 0, time.UTC)
	atlasCollaboratorDecisionAt     = time.Date(2026, time.January, 2, 9, 21, 0, 0, time.UTC)
	atlasOwnerObservationAt         = time.Date(2026, time.January, 2, 9, 30, 0, 0, time.UTC)
	atlasStatusObservationAt        = time.Date(2026, time.January, 2, 9, 31, 0, 0, time.UTC)
	atlasCollaborationObservationAt = time.Date(2026, time.January, 2, 9, 32, 0, 0, time.UTC)
	atlasConcurrentObservationAt    = time.Date(2026, time.January, 2, 9, 33, 0, 0, time.UTC)
	atlasHistoricalCutoff           = time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)
	atlasOwnerCorrectionAt          = time.Date(2026, time.January, 2, 10, 10, 0, 0, time.UTC)
	atlasStatusRetiredAt            = time.Date(2026, time.January, 2, 10, 20, 0, 0, time.UTC)
	atlasConcurrentRetiredAt        = time.Date(2026, time.January, 2, 10, 30, 0, 0, time.UTC)
	atlasValidAt                    = time.Date(2026, time.February, 1, 12, 0, 0, 0, time.UTC)
)

func TestTemporalQueryPostgresCurrentAndAsOfAuthorityRemainIndependent(t *testing.T) {
	fixture := newTemporalQueryPostgresFixture(t)

	asOf := fixture.loadSnapshot(
		t,
		[]identity.EntityID{atlasInitialOwnerID},
		TemporalEntityMatchAll,
		atlasOwnerPredicate,
		&atlasHistoricalCutoff,
	)
	current := fixture.loadSnapshot(
		t,
		[]identity.EntityID{atlasCurrentOwnerID},
		TemporalEntityMatchAll,
		atlasOwnerPredicate,
		nil,
	)

	if len(asOf.Observations) != 1 || len(current.Observations) != 1 {
		t.Fatalf(
			"owner observation counts as-of/current = %d/%d, want 1/1",
			len(asOf.Observations),
			len(current.Observations),
		)
	}
	assertTemporalResolvedEntity(
		t,
		asOf.Observations[0].Subject,
		atlasInitialOwnerID,
	)
	assertTemporalResolvedEntity(
		t,
		current.Observations[0].Subject,
		atlasCurrentOwnerID,
	)
	assertTemporalResolvedEntity(
		t,
		asOf.Observations[0].Object,
		atlasCollaboratorID,
	)
	assertTemporalResolvedEntity(
		t,
		current.Observations[0].Object,
		atlasCollaboratorID,
	)
	if asOf.Observations[0].ObjectGroundingMentionID !=
		string(fixture.collaboratorMention.ID()) ||
		current.Observations[0].ObjectGroundingMentionID !=
			string(fixture.collaboratorMention.ID()) {
		t.Fatalf(
			"owner object grounding as-of/current = %q/%q, want %q",
			asOf.Observations[0].ObjectGroundingMentionID,
			current.Observations[0].ObjectGroundingMentionID,
			fixture.collaboratorMention.ID(),
		)
	}
	if asOf.Observations[0].Observation.ID() != fixture.ownerObservation.ID() ||
		current.Observations[0].Observation.ID() != fixture.ownerObservation.ID() {
		t.Fatalf(
			"owner observation IDs as-of/current = %q/%q, want %q",
			asOf.Observations[0].Observation.ID(),
			current.Observations[0].Observation.ID(),
			fixture.ownerObservation.ID(),
		)
	}
	assertNoTemporalCoverageForObservation(
		t,
		asOf.Coverage,
		fixture.ownerObservation.ID(),
	)
	assertNoTemporalCoverageForObservation(
		t,
		current.Coverage,
		fixture.ownerObservation.ID(),
	)
}

func TestTemporalQueryPostgresLaterSupersessionDoesNotRewriteEarlierCutoff(t *testing.T) {
	fixture := newTemporalQueryPostgresFixture(t)

	asOf := fixture.loadSnapshot(
		t,
		[]identity.EntityID{atlasInitialOwnerID},
		TemporalEntityMatchAll,
		atlasStatusPredicate,
		&atlasHistoricalCutoff,
	)
	current := fixture.loadSnapshot(
		t,
		[]identity.EntityID{atlasCurrentOwnerID},
		TemporalEntityMatchAll,
		atlasStatusPredicate,
		nil,
	)

	if len(asOf.Observations) != 1 ||
		asOf.Observations[0].Observation.ID() != fixture.statusObservation.ID() {
		t.Fatalf(
			"historical status observations = %#v, want admitted %q",
			asOf.Observations,
			fixture.statusObservation.ID(),
		)
	}
	assertNoTemporalCoverageForObservation(
		t,
		asOf.Coverage,
		fixture.statusObservation.ID(),
	)
	if len(current.Observations) != 0 {
		t.Fatalf(
			"current status observations = %#v, want retired observation excluded",
			current.Observations,
		)
	}
	assertTemporalCoverage(
		t,
		current.Coverage,
		TemporalCoverageAuthorityExcluded,
		fixture.statusObservation.ID(),
	)
}

func TestTemporalQueryPostgresEntityMatchAllAndAny(t *testing.T) {
	fixture := newTemporalQueryPostgresFixture(t)

	allReviewedPeople := fixture.loadSnapshot(
		t,
		[]identity.EntityID{atlasCurrentOwnerID, atlasCollaboratorID},
		TemporalEntityMatchAll,
		atlasCollaborationPredicate,
		nil,
	)
	allWithUnrelatedPerson := fixture.loadSnapshot(
		t,
		[]identity.EntityID{atlasCurrentOwnerID, atlasObserverID},
		TemporalEntityMatchAll,
		atlasCollaborationPredicate,
		nil,
	)
	anyWithUnrelatedPerson := fixture.loadSnapshot(
		t,
		[]identity.EntityID{atlasCurrentOwnerID, atlasObserverID},
		TemporalEntityMatchAny,
		atlasCollaborationPredicate,
		nil,
	)
	anyWithOnlyUnrelatedPerson := fixture.loadSnapshot(
		t,
		[]identity.EntityID{atlasObserverID},
		TemporalEntityMatchAny,
		atlasCollaborationPredicate,
		nil,
	)

	if len(allReviewedPeople.Observations) != 1 ||
		allReviewedPeople.Observations[0].Observation.ID() !=
			fixture.collaborationObservation.ID() {
		t.Fatalf(
			"all reviewed-people observations = %#v, want %q",
			allReviewedPeople.Observations,
			fixture.collaborationObservation.ID(),
		)
	}
	if len(allWithUnrelatedPerson.Observations) != 0 {
		t.Fatalf(
			"all with unrelated person observations = %#v, want none",
			allWithUnrelatedPerson.Observations,
		)
	}
	assertTemporalCoverage(
		t,
		allWithUnrelatedPerson.Coverage,
		TemporalCoverageEntityFiltered,
		fixture.collaborationObservation.ID(),
	)
	if len(anyWithUnrelatedPerson.Observations) != 1 ||
		anyWithUnrelatedPerson.Observations[0].Observation.ID() !=
			fixture.collaborationObservation.ID() {
		t.Fatalf(
			"any with unrelated person observations = %#v, want %q",
			anyWithUnrelatedPerson.Observations,
			fixture.collaborationObservation.ID(),
		)
	}
	if len(anyWithOnlyUnrelatedPerson.Observations) != 0 {
		t.Fatalf(
			"any with only unrelated person observations = %#v, want none",
			anyWithOnlyUnrelatedPerson.Observations,
		)
	}
	assertNoTemporalCoverageForObservation(
		t,
		anyWithOnlyUnrelatedPerson.Coverage,
		fixture.collaborationObservation.ID(),
	)
}

func TestTemporalQueryPostgresRoundTripsExactCitationsAndCanonicalTerms(t *testing.T) {
	fixture := newTemporalQueryPostgresFixture(t)
	historical := fixture.loadSnapshot(
		t,
		[]identity.EntityID{atlasInitialOwnerID, atlasCollaboratorID},
		TemporalEntityMatchAll,
		atlasCollaborationPredicate,
		&atlasHistoricalCutoff,
	)
	if len(historical.Observations) != 0 {
		t.Fatalf(
			"historical collaboration observations = %#v, want whole candidate excluded",
			historical.Observations,
		)
	}
	assertTemporalCoverage(
		t,
		historical.Coverage,
		TemporalCoverageAuthorityExcluded,
		fixture.collaborationObservation.ID(),
	)

	snapshot := fixture.loadSnapshot(
		t,
		[]identity.EntityID{atlasCurrentOwnerID, atlasCollaboratorID},
		TemporalEntityMatchAll,
		atlasCollaborationPredicate,
		nil,
	)
	if len(snapshot.Observations) != 1 {
		t.Fatalf(
			"collaboration observation count = %d, want 1",
			len(snapshot.Observations),
		)
	}
	got := snapshot.Observations[0]
	assertTemporalObservationEqual(t, got.Observation, fixture.collaborationObservation)

	statement := got.Observation.Statement()
	if mentionID, ok := statement.Subject.MentionID(); !ok ||
		mentionID != string(fixture.ownerMention.ID()) {
		t.Fatalf(
			"canonical subject = %#v, want mention %q",
			statement.Subject,
			fixture.ownerMention.ID(),
		)
	}
	if entityID, groundingID, ok := statement.Object.Entity(); !ok ||
		entityID != string(atlasCollaboratorID) ||
		groundingID != string(fixture.collaboratorMention.ID()) {
		t.Fatalf(
			"canonical object = %#v, want grounded entity %q/%q",
			statement.Object,
			atlasCollaboratorID,
			fixture.collaboratorMention.ID(),
		)
	}
	assertTemporalResolvedEntity(t, got.Subject, atlasCurrentOwnerID)
	assertTemporalResolvedEntity(t, got.Object, atlasCollaboratorID)
	if got.SubjectGroundingMentionID != string(fixture.ownerMention.ID()) ||
		got.ObjectGroundingMentionID != string(fixture.collaboratorMention.ID()) {
		t.Fatalf(
			"resolved grounding IDs = %q/%q, want %q/%q",
			got.SubjectGroundingMentionID,
			got.ObjectGroundingMentionID,
			fixture.ownerMention.ID(),
			fixture.collaboratorMention.ID(),
		)
	}

	expectedCitations := map[evidence.EvidenceID]TemporalEvidenceRecord{
		fixture.supportingSpan.ID(): {
			EvidenceID:        fixture.supportingSpan.ID(),
			Role:              observation.EvidenceSupporting,
			SourceDocumentID:  fixture.documentRef.Ref.SourceDocumentID,
			DocumentVersionID: fixture.documentRef.Ref.VersionID,
			SectionID:         "section:project-atlas/decision-log",
			SectionTitle:      "Project Atlas decision log",
			SectionPath:       []string{"Project Atlas", "Decision log"},
			SectionOrder:      0,
			SectionRole:       "decision-log",
			StartOffset:       atlasSupportingStartOffset,
			EndOffset:         atlasSupportingEndOffset,
			Locator:           "synthetic://project-atlas/authority-history",
			Text:              atlasSupportingQuote,
		},
		fixture.contradictingSpan.ID(): {
			EvidenceID:        fixture.contradictingSpan.ID(),
			Role:              observation.EvidenceContradicting,
			SourceDocumentID:  fixture.documentRef.Ref.SourceDocumentID,
			DocumentVersionID: fixture.documentRef.Ref.VersionID,
			SectionID:         "section:project-atlas/review-note",
			SectionTitle:      "Project Atlas review note",
			SectionPath:       []string{"Project Atlas", "Review note"},
			SectionOrder:      1,
			SectionRole:       "review-note",
			StartOffset:       atlasContradictingStartOffset,
			EndOffset:         atlasContradictingEndOffset,
			Locator:           "synthetic://project-atlas/authority-history",
			Text:              atlasContradictingQuote,
		},
	}
	if len(got.Evidence) != len(expectedCitations) {
		t.Fatalf(
			"citation count = %d, want %d",
			len(got.Evidence),
			len(expectedCitations),
		)
	}
	seenCitations := make(map[evidence.EvidenceID]int, len(expectedCitations))
	for _, citation := range got.Evidence {
		want, exists := expectedCitations[citation.EvidenceID]
		if !exists {
			t.Fatalf("unexpected citation evidence ID %q", citation.EvidenceID)
		}
		seenCitations[citation.EvidenceID]++
		if seenCitations[citation.EvidenceID] != 1 {
			t.Fatalf(
				"citation evidence ID %q occurred %d times, want exactly once",
				citation.EvidenceID,
				seenCitations[citation.EvidenceID],
			)
		}
		if !reflect.DeepEqual(citation, want) {
			t.Fatalf(
				"citation %q did not round-trip exactly:\ngot  %#v\nwant %#v",
				citation.EvidenceID,
				citation,
				want,
			)
		}
	}
	for evidenceID := range expectedCitations {
		if seenCitations[evidenceID] != 1 {
			t.Fatalf(
				"citation evidence ID %q occurred %d times, want exactly once",
				evidenceID,
				seenCitations[evidenceID],
			)
		}
	}
}

func TestTemporalQueryPostgresSourceVersionCutoffRejectsWholeCitationSet(t *testing.T) {
	fixture := newTemporalQueryPostgresFixture(t)
	historical := fixture.loadSnapshot(
		t,
		[]identity.EntityID{atlasInitialOwnerID, atlasCollaboratorID},
		TemporalEntityMatchAll,
		atlasLateSourcePredicate,
		&atlasHistoricalCutoff,
	)
	current := fixture.loadSnapshot(
		t,
		[]identity.EntityID{atlasCurrentOwnerID, atlasCollaboratorID},
		TemporalEntityMatchAll,
		atlasLateSourcePredicate,
		nil,
	)

	if len(historical.Observations) != 0 {
		t.Fatalf(
			"historical late-source observations = %#v, want whole candidate excluded",
			historical.Observations,
		)
	}
	assertTemporalCoverage(
		t,
		historical.Coverage,
		TemporalCoverageAuthorityExcluded,
		fixture.lateSourceObservation.ID(),
	)
	if len(current.Observations) != 1 ||
		current.Observations[0].Observation.ID() !=
			fixture.lateSourceObservation.ID() {
		t.Fatalf(
			"current late-source observations = %#v, want %q",
			current.Observations,
			fixture.lateSourceObservation.ID(),
		)
	}
	assertTemporalEvidenceIDsExactlyOnce(
		t,
		current.Observations[0].Evidence,
		fixture.supportingSpan.ID(),
		fixture.lateSourceSpan.ID(),
	)
}

func TestTemporalQueryPostgresUsesOneCoherentSnapshotDuringConcurrentAuthorityChange(t *testing.T) {
	fixture := newTemporalQueryPostgresFixture(t)
	selection := fixture.selection(
		[]identity.EntityID{atlasCurrentOwnerID},
		TemporalEntityMatchAll,
		atlasConcurrentPredicate,
		nil,
	)
	firstAuthorityRead := make(chan struct{})
	releaseReader := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(releaseReader)
		})
	}
	defer release()

	type snapshotResult struct {
		snapshot TemporalQuerySnapshot
		err      error
	}
	result := make(chan snapshotResult, 1)
	go func() {
		snapshot, err := loadTemporalQuerySnapshot(
			fixture.ctx,
			temporalPausingBeginner{
				beginner:           fixture.database.pool,
				firstAuthorityRead: firstAuthorityRead,
				releaseReader:      releaseReader,
			},
			selection,
			nil,
		)
		result <- snapshotResult{snapshot: snapshot, err: err}
	}()

	select {
	case <-firstAuthorityRead:
	case <-fixture.ctx.Done():
		t.Fatalf("wait for first authority read: %v", fixture.ctx.Err())
	}

	retired := mustTemporalAdmissionDecision(t, admission.DecisionInput{
		ID:           "admission:project-atlas/concurrent-retired",
		TargetKind:   admission.TargetObservation,
		TargetID:     string(fixture.concurrentObservation.ID()),
		Outcome:      admission.Retired,
		ReasonCode:   "reviewed_authority_change",
		Authority:    admission.AuthorityReviewer,
		RecordedAt:   atlasConcurrentRetiredAt,
		SupersedesID: fixture.concurrentAdmission.ID(),
	})
	if err := fixture.database.InTransaction(
		fixture.ctx,
		func(transaction *Transaction) error {
			return transaction.AppendAdmissionDecision(fixture.ctx, retired)
		},
	); err != nil {
		t.Fatalf("commit concurrent authority change: %v", err)
	}
	release()

	var first snapshotResult
	select {
	case first = <-result:
	case <-fixture.ctx.Done():
		t.Fatalf("wait for coherent snapshot result: %v", fixture.ctx.Err())
	}
	if first.err != nil {
		t.Fatalf("concurrent LoadTemporalQuerySnapshot() error = %v", first.err)
	}
	if len(first.snapshot.Observations) != 1 ||
		first.snapshot.Observations[0].Observation.ID() !=
			fixture.concurrentObservation.ID() {
		t.Fatalf(
			"first concurrent snapshot = %#v, want coherent pre-change authority",
			first.snapshot,
		)
	}
	assertNoTemporalCoverageForObservation(
		t,
		first.snapshot.Coverage,
		fixture.concurrentObservation.ID(),
	)

	next, err := fixture.database.LoadTemporalQuerySnapshot(
		fixture.ctx,
		selection,
		nil,
	)
	if err != nil {
		t.Fatalf("next LoadTemporalQuerySnapshot() error = %v", err)
	}
	if len(next.Observations) != 0 {
		t.Fatalf(
			"next snapshot observations = %#v, want retired observation excluded",
			next.Observations,
		)
	}
	assertTemporalCoverage(
		t,
		next.Coverage,
		TemporalCoverageAuthorityExcluded,
		fixture.concurrentObservation.ID(),
	)
}

func TestTemporalQueryPostgresPerformsNoWritesOrLeaseClaims(t *testing.T) {
	fixture := newTemporalQueryPostgresFixture(t)
	before := fixture.mutationSnapshot(t)
	if before["extraction_runs"].count != 0 ||
		before["extraction_attempts"].count != 0 {
		t.Fatalf(
			"synthetic fixture extraction run/attempt counts = %d/%d, want 0/0",
			before["extraction_runs"].count,
			before["extraction_attempts"].count,
		)
	}

	fixture.loadSnapshot(
		t,
		[]identity.EntityID{atlasInitialOwnerID},
		TemporalEntityMatchAll,
		atlasOwnerPredicate,
		&atlasHistoricalCutoff,
	)
	fixture.loadSnapshot(
		t,
		[]identity.EntityID{atlasCurrentOwnerID},
		TemporalEntityMatchAll,
		atlasOwnerPredicate,
		nil,
	)

	after := fixture.mutationSnapshot(t)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf(
			"canonical PostgreSQL mutation fingerprint changed across read-only queries:\nbefore %#v\nafter  %#v",
			before,
			after,
		)
	}
}

type temporalQueryPostgresFixture struct {
	ctx               context.Context
	database          *Database
	admin             *pgx.Conn
	document          evidence.DocumentVersion
	documentRef       PutDocumentVersionResult
	sections          []evidence.Section
	supportingSpan    evidence.EvidenceSpan
	contradictingSpan evidence.EvidenceSpan
	lateSourceSpan    evidence.EvidenceSpan

	ownerMention        identity.MentionRecord
	collaboratorMention identity.MentionRecord

	ownerObservation         observation.Observation
	statusObservation        observation.Observation
	collaborationObservation observation.Observation
	lateSourceObservation    observation.Observation
	concurrentObservation    observation.Observation
	concurrentAdmission      admission.Decision

	selectionValue temporal.TemporalSelection
}

func newTemporalQueryPostgresFixture(t testing.TB) temporalQueryPostgresFixture {
	t.Helper()
	isolated := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), temporalQueryPostgresTimeout)
	t.Cleanup(cancel)

	manifest, err := coremigrations.Manifest()
	if err != nil {
		t.Fatalf("coremigrations.Manifest() error = %v", err)
	}
	applicationConfig, err := pgx.ParseConfig(isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("parse isolated application PostgreSQL configuration: %v", err)
	}
	if _, err := (migration.Migrator{
		DatabaseURL:     isolated.AdminURL(),
		ApplicationRole: applicationConfig.User,
		Manifests:       []migration.Manifest{manifest},
	}).Apply(ctx); err != nil {
		t.Fatalf("apply canonical core migrations: %v", err)
	}

	database, err := Open(ctx, isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(database.Close)
	admin, err := pgx.Connect(ctx, isolated.AdminURL())
	if err != nil {
		t.Fatalf("connect isolated migration principal: %v", err)
	}
	t.Cleanup(func() {
		_ = admin.Close(context.Background())
	})

	fixture := temporalQueryPostgresFixture{
		ctx:      ctx,
		database: database,
		admin:    admin,
	}
	fixture.seed(t)
	return fixture
}

func (fixture *temporalQueryPostgresFixture) seed(t testing.TB) {
	t.Helper()
	fixture.sections = []evidence.Section{
		mustTemporalSection(t, evidence.SectionInput{
			ID:    "section:project-atlas/decision-log",
			Title: "Project Atlas decision log",
			Path:  []string{"Project Atlas", "Decision log"},
			Order: 0,
			Role:  "decision-log",
			Text:  atlasSupportingSectionText,
		}),
		mustTemporalSection(t, evidence.SectionInput{
			ID:    "section:project-atlas/review-note",
			Title: "Project Atlas review note",
			Path:  []string{"Project Atlas", "Review note"},
			Order: 1,
			Role:  "review-note",
			Text:  atlasContradictingSectionText,
		}),
	}
	fixture.document = mustTemporalDocument(t, evidence.DocumentVersionInput{
		Provider:           "synthetic-project-atlas",
		ProviderDocumentID: "project-atlas-authority-history",
		Title:              "Synthetic Project Atlas authority history",
		Locator:            "synthetic://project-atlas/authority-history",
		ProviderVersion:    "synthetic-v1",
		ModifiedAt:         atlasSourceTime,
		RecordedAt:         atlasDocumentRecordedAt,
		SourceTime:         &atlasSourceTime,
		Sections:           fixture.sections,
	})
	var err error
	fixture.documentRef, err = fixture.database.PutDocumentVersion(
		fixture.ctx,
		fixture.document,
	)
	if err != nil {
		t.Fatalf("PutDocumentVersion() error = %v", err)
	}
	fixture.supportingSpan = mustTemporalSpan(
		t,
		fixture.document,
		fixture.sections[0],
		atlasSupportingStartOffset,
		atlasSupportingEndOffset,
		atlasSupportingQuote,
		atlasDocumentRecordedAt.Add(time.Minute),
	)
	fixture.contradictingSpan = mustTemporalSpan(
		t,
		fixture.document,
		fixture.sections[1],
		atlasContradictingStartOffset,
		atlasContradictingEndOffset,
		atlasContradictingQuote,
		atlasHistoricalCutoff.Add(time.Minute),
	)
	if err := fixture.database.InTransaction(
		fixture.ctx,
		func(transaction *Transaction) error {
			if err := transaction.SetCurrentDocumentVersion(
				fixture.ctx,
				fixture.documentRef.Ref.SourceDocumentID,
				fixture.documentRef.Ref.VersionID,
			); err != nil {
				return err
			}
			for _, span := range []evidence.EvidenceSpan{
				fixture.supportingSpan,
				fixture.contradictingSpan,
			} {
				created, err := transaction.PutEvidenceSpan(fixture.ctx, span)
				if err != nil {
					return err
				}
				if !created {
					return fmt.Errorf("evidence span %q was not created", span.ID())
				}
			}
			return nil
		},
	); err != nil {
		t.Fatalf("persist Project Atlas document evidence: %v", err)
	}
	lateSourceTime := atlasSourceTime.Add(time.Minute)
	lateSourceSection := mustTemporalSection(t, evidence.SectionInput{
		ID:    "section:project-atlas/late-source",
		Title: "Project Atlas later source",
		Path:  []string{"Project Atlas", "Later source"},
		Order: 0,
		Role:  "review-note",
		Text:  atlasLateSourceSectionText,
	})
	lateSourceDocument := mustTemporalDocument(t, evidence.DocumentVersionInput{
		Provider:           "synthetic-project-atlas",
		ProviderDocumentID: "project-atlas-late-source",
		Title:              "Synthetic Project Atlas later source",
		Locator:            "synthetic://project-atlas/late-source",
		ProviderVersion:    "synthetic-v1",
		ModifiedAt:         lateSourceTime,
		RecordedAt:         atlasHistoricalCutoff.Add(time.Minute),
		SourceTime:         &lateSourceTime,
		Sections:           []evidence.Section{lateSourceSection},
	})
	lateSourceRef, err := fixture.database.PutDocumentVersion(
		fixture.ctx,
		lateSourceDocument,
	)
	if err != nil {
		t.Fatalf("PutDocumentVersion(late source) error = %v", err)
	}
	fixture.lateSourceSpan = mustTemporalSpan(
		t,
		lateSourceDocument,
		lateSourceSection,
		atlasLateSourceStartOffset,
		atlasLateSourceEndOffset,
		atlasLateSourceQuote,
		atlasHistoricalCutoff.Add(-time.Minute),
	)
	if err := fixture.database.InTransaction(
		fixture.ctx,
		func(transaction *Transaction) error {
			if err := transaction.SetCurrentDocumentVersion(
				fixture.ctx,
				lateSourceRef.Ref.SourceDocumentID,
				lateSourceRef.Ref.VersionID,
			); err != nil {
				return err
			}
			created, err := transaction.PutEvidenceSpan(
				fixture.ctx,
				fixture.lateSourceSpan,
			)
			if err != nil {
				return err
			}
			if !created {
				return fmt.Errorf(
					"late-source evidence span %q was not created",
					fixture.lateSourceSpan.ID(),
				)
			}
			return nil
		},
	); err != nil {
		t.Fatalf("persist Project Atlas late-source evidence: %v", err)
	}

	initialOwner := mustTemporalEntity(
		t,
		atlasInitialOwnerID,
		"Alex Initial",
	)
	currentOwner := mustTemporalEntity(
		t,
		atlasCurrentOwnerID,
		"Alex Current",
	)
	collaborator := mustTemporalEntity(
		t,
		atlasCollaboratorID,
		"Blair Collaborator",
	)
	observer := mustTemporalEntity(
		t,
		atlasObserverID,
		"Casey Observer",
	)
	fixture.ownerMention = mustTemporalMention(t, identity.MentionInput{
		ID:              "mention:project-atlas/alex",
		EvidenceID:      fixture.supportingSpan.ID(),
		DerivationRunID: "run:synthetic/project-atlas-identity",
		Surface:         "Alex",
		NormalizedName:  identity.NormalizeName("Alex"),
		Role:            "owner",
		RecordedAt:      atlasOwnerMentionRecordedAt,
	})
	fixture.collaboratorMention = mustTemporalMention(t, identity.MentionInput{
		ID:              "mention:project-atlas/blair",
		EvidenceID:      fixture.supportingSpan.ID(),
		DerivationRunID: "run:synthetic/project-atlas-identity",
		Surface:         "Blair",
		NormalizedName:  identity.NormalizeName("Blair"),
		Role:            "collaborator",
		RecordedAt:      atlasCollaboratorMentionAt,
	})
	ownerProposal := mustTemporalProposal(t, identity.ResolutionProposalInput{
		ID:          "proposal:project-atlas/alex",
		MentionID:   fixture.ownerMention.ID(),
		ReasonCode:  "reviewed_identity",
		EvidenceIDs: []evidence.EvidenceID{fixture.supportingSpan.ID()},
		RecordedAt:  atlasOwnerProposalRecordedAt,
	})
	collaboratorProposal := mustTemporalProposal(t, identity.ResolutionProposalInput{
		ID:          "proposal:project-atlas/blair",
		MentionID:   fixture.collaboratorMention.ID(),
		ReasonCode:  "reviewed_identity",
		EvidenceIDs: []evidence.EvidenceID{fixture.supportingSpan.ID()},
		RecordedAt:  atlasCollaboratorProposalAt,
	})
	ownerInitialDecision := mustTemporalResolutionDecision(
		t,
		identity.ResolutionDecisionInput{
			ID:         "decision:project-atlas/alex-initial",
			ProposalID: ownerProposal.ID(),
			Outcome:    identity.DecisionAccepted,
			EntityID:   initialOwner.ID(),
			Authority:  identity.AuthorityReviewer,
			ReasonCode: "reviewed_identity",
			RecordedAt: atlasOwnerInitialDecisionAt,
		},
	)
	ownerCorrection := mustTemporalResolutionDecision(
		t,
		identity.ResolutionDecisionInput{
			ID:           "decision:project-atlas/alex-corrected",
			ProposalID:   ownerProposal.ID(),
			Outcome:      identity.DecisionAccepted,
			EntityID:     currentOwner.ID(),
			Authority:    identity.AuthorityReviewer,
			ReasonCode:   "reviewed_identity_correction",
			RecordedAt:   atlasOwnerCorrectionAt,
			SupersedesID: ownerInitialDecision.ID(),
		},
	)
	collaboratorDecision := mustTemporalResolutionDecision(
		t,
		identity.ResolutionDecisionInput{
			ID:         "decision:project-atlas/blair",
			ProposalID: collaboratorProposal.ID(),
			Outcome:    identity.DecisionAccepted,
			EntityID:   collaborator.ID(),
			Authority:  identity.AuthorityReviewer,
			ReasonCode: "reviewed_identity",
			RecordedAt: atlasCollaboratorDecisionAt,
		},
	)
	identityAdmissions := []admission.Decision{
		mustTemporalAdmissionDecision(t, admission.DecisionInput{
			ID:         "admission:project-atlas/alex-mention",
			TargetKind: admission.TargetMention,
			TargetID:   string(fixture.ownerMention.ID()),
			Outcome:    admission.Admitted,
			ReasonCode: "reviewed_identity",
			Authority:  admission.AuthorityReviewer,
			RecordedAt: atlasOwnerInitialDecisionAt.Add(2 * time.Minute),
		}),
		mustTemporalAdmissionDecision(t, admission.DecisionInput{
			ID:         "admission:project-atlas/blair-mention",
			TargetKind: admission.TargetMention,
			TargetID:   string(fixture.collaboratorMention.ID()),
			Outcome:    admission.Admitted,
			ReasonCode: "reviewed_identity",
			Authority:  admission.AuthorityReviewer,
			RecordedAt: atlasOwnerInitialDecisionAt.Add(3 * time.Minute),
		}),
		mustTemporalAdmissionDecision(t, admission.DecisionInput{
			ID:         "admission:project-atlas/alex-initial-decision",
			TargetKind: admission.TargetIdentityDecision,
			TargetID:   string(ownerInitialDecision.ID()),
			Outcome:    admission.Admitted,
			ReasonCode: "reviewed_identity",
			Authority:  admission.AuthorityReviewer,
			RecordedAt: atlasOwnerInitialDecisionAt.Add(4 * time.Minute),
		}),
		mustTemporalAdmissionDecision(t, admission.DecisionInput{
			ID:         "admission:project-atlas/blair-decision",
			TargetKind: admission.TargetIdentityDecision,
			TargetID:   string(collaboratorDecision.ID()),
			Outcome:    admission.Admitted,
			ReasonCode: "reviewed_identity",
			Authority:  admission.AuthorityReviewer,
			RecordedAt: atlasOwnerInitialDecisionAt.Add(5 * time.Minute),
		}),
		mustTemporalAdmissionDecision(t, admission.DecisionInput{
			ID:         "admission:project-atlas/alex-corrected-decision",
			TargetKind: admission.TargetIdentityDecision,
			TargetID:   string(ownerCorrection.ID()),
			Outcome:    admission.Admitted,
			ReasonCode: "reviewed_identity_correction",
			Authority:  admission.AuthorityReviewer,
			RecordedAt: atlasOwnerCorrectionAt.Add(time.Minute),
		}),
	}
	if err := fixture.database.InTransaction(
		fixture.ctx,
		func(transaction *Transaction) error {
			for _, entity := range []identity.Entity{
				initialOwner,
				currentOwner,
				collaborator,
				observer,
			} {
				if _, err := transaction.PutEntity(fixture.ctx, entity); err != nil {
					return err
				}
			}
			for _, mention := range []identity.MentionRecord{
				fixture.ownerMention,
				fixture.collaboratorMention,
			} {
				if _, err := transaction.PutMention(fixture.ctx, mention); err != nil {
					return err
				}
			}
			for _, proposal := range []identity.ResolutionProposal{
				ownerProposal,
				collaboratorProposal,
			} {
				if _, err := transaction.PutResolutionProposal(
					fixture.ctx,
					proposal,
				); err != nil {
					return err
				}
			}
			for _, decision := range []identity.ResolutionDecision{
				ownerInitialDecision,
				ownerCorrection,
				collaboratorDecision,
			} {
				if err := transaction.AppendResolutionDecision(
					fixture.ctx,
					decision,
					nil,
				); err != nil {
					return err
				}
			}
			for _, decision := range identityAdmissions {
				if err := transaction.AppendAdmissionDecision(
					fixture.ctx,
					decision,
				); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		t.Fatalf("persist Project Atlas identity authority: %v", err)
	}

	subject := mustTemporalMentionTerm(t, fixture.ownerMention.ID())
	reviewedCollaborator := mustTemporalMentionTerm(
		t,
		fixture.collaboratorMention.ID(),
	)
	groundedCollaborator := mustTemporalEntityTerm(
		t,
		atlasCollaboratorID,
		fixture.collaboratorMention.ID(),
	)
	fixture.ownerObservation = mustTemporalObservation(
		t,
		observation.ObservationInput{
			ID: "observation:project-atlas/owner",
			Statement: observation.Statement{
				Subject:   subject,
				Predicate: atlasOwnerPredicate,
				Object:    reviewedCollaborator,
			},
			ValidTime:  mustTemporalInstant(t, atlasValidAt),
			RecordedAt: atlasOwnerObservationAt,
			Evidence: []observation.EvidenceLink{{
				EvidenceID: fixture.supportingSpan.ID(),
				Role:       observation.EvidenceSupporting,
			}},
			Derivation: observation.Derivation{
				Method:  "synthetic-review",
				Version: "project-atlas-v1",
			},
			Status: observation.StatusObserved,
		},
	)
	fixture.statusObservation = mustTemporalObservation(
		t,
		observation.ObservationInput{
			ID: "observation:project-atlas/status",
			Statement: observation.Statement{
				Subject:   subject,
				Predicate: atlasStatusPredicate,
				Object:    reviewedCollaborator,
			},
			ValidTime:  mustTemporalInstant(t, atlasValidAt),
			RecordedAt: atlasStatusObservationAt,
			Evidence: []observation.EvidenceLink{{
				EvidenceID: fixture.supportingSpan.ID(),
				Role:       observation.EvidenceSupporting,
			}},
			Derivation: observation.Derivation{
				Method:  "synthetic-review",
				Version: "project-atlas-v1",
			},
			Status: observation.StatusObserved,
		},
	)
	fixture.collaborationObservation = mustTemporalObservation(
		t,
		observation.ObservationInput{
			ID: "observation:project-atlas/collaboration",
			Statement: observation.Statement{
				Subject:   subject,
				Predicate: atlasCollaborationPredicate,
				Object:    groundedCollaborator,
			},
			ValidTime:  mustTemporalInstant(t, atlasValidAt),
			RecordedAt: atlasCollaborationObservationAt,
			Evidence: []observation.EvidenceLink{
				{
					EvidenceID: fixture.contradictingSpan.ID(),
					Role:       observation.EvidenceContradicting,
				},
				{
					EvidenceID: fixture.supportingSpan.ID(),
					Role:       observation.EvidenceSupporting,
				},
			},
			Derivation: observation.Derivation{
				Method:  "synthetic-review",
				Version: "project-atlas-v1",
			},
			Status: observation.StatusValidatedEmpirically,
		},
	)
	fixture.lateSourceObservation = mustTemporalObservation(
		t,
		observation.ObservationInput{
			ID: "observation:project-atlas/late-source",
			Statement: observation.Statement{
				Subject:   subject,
				Predicate: atlasLateSourcePredicate,
				Object:    groundedCollaborator,
			},
			ValidTime:  mustTemporalInstant(t, atlasValidAt),
			RecordedAt: atlasCollaborationObservationAt.Add(time.Minute),
			Evidence: []observation.EvidenceLink{
				{
					EvidenceID: fixture.supportingSpan.ID(),
					Role:       observation.EvidenceSupporting,
				},
				{
					EvidenceID: fixture.lateSourceSpan.ID(),
					Role:       observation.EvidenceContradicting,
				},
			},
			Derivation: observation.Derivation{
				Method:  "synthetic-review",
				Version: "project-atlas-v1",
			},
			Status: observation.StatusObserved,
		},
	)
	fixture.concurrentObservation = mustTemporalObservation(
		t,
		observation.ObservationInput{
			ID: "observation:project-atlas/concurrent",
			Statement: observation.Statement{
				Subject:   subject,
				Predicate: atlasConcurrentPredicate,
				Object:    reviewedCollaborator,
			},
			ValidTime:  mustTemporalInstant(t, atlasValidAt),
			RecordedAt: atlasConcurrentObservationAt,
			Evidence: []observation.EvidenceLink{{
				EvidenceID: fixture.supportingSpan.ID(),
				Role:       observation.EvidenceSupporting,
			}},
			Derivation: observation.Derivation{
				Method:  "synthetic-review",
				Version: "project-atlas-v1",
			},
			Status: observation.StatusObserved,
		},
	)
	ownerAdmission := mustTemporalAdmissionDecision(t, admission.DecisionInput{
		ID:         "admission:project-atlas/owner",
		TargetKind: admission.TargetObservation,
		TargetID:   string(fixture.ownerObservation.ID()),
		Outcome:    admission.Admitted,
		ReasonCode: "reviewed_observation",
		Authority:  admission.AuthorityReviewer,
		RecordedAt: atlasOwnerObservationAt.Add(4 * time.Minute),
	})
	statusAdmission := mustTemporalAdmissionDecision(t, admission.DecisionInput{
		ID:         "admission:project-atlas/status-admitted",
		TargetKind: admission.TargetObservation,
		TargetID:   string(fixture.statusObservation.ID()),
		Outcome:    admission.Admitted,
		ReasonCode: "reviewed_observation",
		Authority:  admission.AuthorityReviewer,
		RecordedAt: atlasStatusObservationAt.Add(4 * time.Minute),
	})
	statusRetired := mustTemporalAdmissionDecision(t, admission.DecisionInput{
		ID:           "admission:project-atlas/status-retired",
		TargetKind:   admission.TargetObservation,
		TargetID:     string(fixture.statusObservation.ID()),
		Outcome:      admission.Retired,
		ReasonCode:   "reviewed_authority_change",
		Authority:    admission.AuthorityReviewer,
		RecordedAt:   atlasStatusRetiredAt,
		SupersedesID: statusAdmission.ID(),
	})
	collaborationAdmission := mustTemporalAdmissionDecision(
		t,
		admission.DecisionInput{
			ID:         "admission:project-atlas/collaboration",
			TargetKind: admission.TargetObservation,
			TargetID:   string(fixture.collaborationObservation.ID()),
			Outcome:    admission.Admitted,
			ReasonCode: "reviewed_observation",
			Authority:  admission.AuthorityReviewer,
			RecordedAt: atlasCollaborationObservationAt.Add(4 * time.Minute),
		},
	)
	lateSourceAdmission := mustTemporalAdmissionDecision(
		t,
		admission.DecisionInput{
			ID:         "admission:project-atlas/late-source",
			TargetKind: admission.TargetObservation,
			TargetID:   string(fixture.lateSourceObservation.ID()),
			Outcome:    admission.Admitted,
			ReasonCode: "reviewed_observation",
			Authority:  admission.AuthorityReviewer,
			RecordedAt: atlasCollaborationObservationAt.Add(5 * time.Minute),
		},
	)
	fixture.concurrentAdmission = mustTemporalAdmissionDecision(
		t,
		admission.DecisionInput{
			ID:         "admission:project-atlas/concurrent-admitted",
			TargetKind: admission.TargetObservation,
			TargetID:   string(fixture.concurrentObservation.ID()),
			Outcome:    admission.Admitted,
			ReasonCode: "reviewed_observation",
			Authority:  admission.AuthorityReviewer,
			RecordedAt: atlasConcurrentObservationAt.Add(4 * time.Minute),
		},
	)
	if err := fixture.database.InTransaction(
		fixture.ctx,
		func(transaction *Transaction) error {
			for _, value := range []observation.Observation{
				fixture.ownerObservation,
				fixture.statusObservation,
				fixture.collaborationObservation,
				fixture.lateSourceObservation,
				fixture.concurrentObservation,
			} {
				if _, err := transaction.PutObservation(
					fixture.ctx,
					value,
				); err != nil {
					return err
				}
			}
			for _, decision := range []admission.Decision{
				ownerAdmission,
				statusAdmission,
				statusRetired,
				collaborationAdmission,
				lateSourceAdmission,
				fixture.concurrentAdmission,
			} {
				if err := transaction.AppendAdmissionDecision(
					fixture.ctx,
					decision,
				); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		t.Fatalf("persist Project Atlas observations and authority: %v", err)
	}

	fixture.selectionValue, err = temporal.At("project-atlas", atlasValidAt)
	if err != nil {
		t.Fatalf("temporal.At() error = %v", err)
	}
}

func (fixture temporalQueryPostgresFixture) selection(
	entityIDs []identity.EntityID,
	match TemporalEntityMatch,
	predicate observation.Predicate,
	cutoff *time.Time,
) TemporalQuerySelection {
	return TemporalQuerySelection{
		EntityIDs:     append([]identity.EntityID(nil), entityIDs...),
		EntityMatch:   match,
		Predicates:    []observation.Predicate{predicate},
		Selections:    []temporal.TemporalSelection{fixture.selectionValue},
		KnowledgeAsOf: cutoff,
	}
}

func (fixture temporalQueryPostgresFixture) loadSnapshot(
	t testing.TB,
	entityIDs []identity.EntityID,
	match TemporalEntityMatch,
	predicate observation.Predicate,
	cutoff *time.Time,
) TemporalQuerySnapshot {
	t.Helper()
	snapshot, err := fixture.database.LoadTemporalQuerySnapshot(
		fixture.ctx,
		fixture.selection(entityIDs, match, predicate, cutoff),
		nil,
	)
	if err != nil {
		t.Fatalf("LoadTemporalQuerySnapshot() error = %v", err)
	}
	return snapshot
}

type temporalMutationRecord struct {
	count       int64
	fingerprint string
}

func (fixture temporalQueryPostgresFixture) mutationSnapshot(
	t testing.TB,
) map[string]temporalMutationRecord {
	t.Helper()
	rows, err := fixture.admin.Query(fixture.ctx, temporalMutationSnapshotSQL)
	if err != nil {
		t.Fatalf("read canonical mutation snapshot: %v", err)
	}
	defer rows.Close()
	result := make(map[string]temporalMutationRecord)
	for rows.Next() {
		var relation string
		var record temporalMutationRecord
		if err := rows.Scan(
			&relation,
			&record.count,
			&record.fingerprint,
		); err != nil {
			t.Fatalf("scan canonical mutation snapshot: %v", err)
		}
		result[relation] = record
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate canonical mutation snapshot: %v", err)
	}
	return result
}

const temporalMutationSnapshotSQL = `
	SELECT 'source_documents', count(*),
		coalesce(md5(string_agg(id || ':' || xmin::text, ',' ORDER BY id)), md5(''))
	FROM stacks_core.source_documents
	UNION ALL
	SELECT 'document_versions', count(*),
		coalesce(md5(string_agg(id || ':' || xmin::text, ',' ORDER BY id)), md5(''))
	FROM stacks_core.document_versions
	UNION ALL
	SELECT 'source_revision_observations', count(*),
		coalesce(md5(string_agg(id || ':' || xmin::text, ',' ORDER BY id)), md5(''))
	FROM stacks_core.source_revision_observations
	UNION ALL
	SELECT 'document_sections', count(*),
		coalesce(md5(string_agg(
			document_version_id || ':' || section_id || ':' || xmin::text,
			',' ORDER BY document_version_id, section_id
		)), md5(''))
	FROM stacks_core.document_sections
	UNION ALL
	SELECT 'evidence_spans', count(*),
		coalesce(md5(string_agg(id || ':' || xmin::text, ',' ORDER BY id)), md5(''))
	FROM stacks_core.evidence_spans
	UNION ALL
	SELECT 'entities', count(*),
		coalesce(md5(string_agg(id || ':' || xmin::text, ',' ORDER BY id)), md5(''))
	FROM stacks_core.entities
	UNION ALL
	SELECT 'mentions', count(*),
		coalesce(md5(string_agg(id || ':' || xmin::text, ',' ORDER BY id)), md5(''))
	FROM stacks_core.mentions
	UNION ALL
	SELECT 'resolution_proposals', count(*),
		coalesce(md5(string_agg(id || ':' || xmin::text, ',' ORDER BY id)), md5(''))
	FROM stacks_core.resolution_proposals
	UNION ALL
	SELECT 'resolution_proposal_evidence', count(*),
		coalesce(md5(string_agg(
			proposal_id || ':' || evidence_id || ':' || xmin::text,
			',' ORDER BY proposal_id, evidence_id
		)), md5(''))
	FROM stacks_core.resolution_proposal_evidence
	UNION ALL
	SELECT 'resolution_candidates', count(*),
		coalesce(md5(string_agg(id || ':' || xmin::text, ',' ORDER BY id)), md5(''))
	FROM stacks_core.resolution_candidates
	UNION ALL
	SELECT 'resolution_decisions', count(*),
		coalesce(md5(string_agg(id || ':' || xmin::text, ',' ORDER BY id)), md5(''))
	FROM stacks_core.resolution_decisions
	UNION ALL
	SELECT 'entity_alias_assertions', count(*),
		coalesce(md5(string_agg(id || ':' || xmin::text, ',' ORDER BY id)), md5(''))
	FROM stacks_core.entity_alias_assertions
	UNION ALL
	SELECT 'admission_targets', count(*),
		coalesce(md5(string_agg(
			target_kind || ':' || target_id || ':' || xmin::text,
			',' ORDER BY target_kind, target_id
		)), md5(''))
	FROM stacks_core.admission_targets
	UNION ALL
	SELECT 'admission_decisions', count(*),
		coalesce(md5(string_agg(id || ':' || xmin::text, ',' ORDER BY id)), md5(''))
	FROM stacks_core.admission_decisions
	UNION ALL
	SELECT 'extraction_runs', count(*),
		coalesce(md5(string_agg(id || ':' || xmin::text, ',' ORDER BY id)), md5(''))
	FROM stacks_core.extraction_runs
	UNION ALL
	SELECT 'extraction_attempts', count(*),
		coalesce(md5(string_agg(id || ':' || xmin::text, ',' ORDER BY id)), md5(''))
	FROM stacks_core.extraction_attempts
	UNION ALL
	SELECT 'observations', count(*),
		coalesce(md5(string_agg(id || ':' || xmin::text, ',' ORDER BY id)), md5(''))
	FROM stacks_core.observations
	UNION ALL
	SELECT 'observation_evidence', count(*),
		coalesce(md5(string_agg(
			observation_id || ':' || evidence_id || ':' || role || ':' || xmin::text,
			',' ORDER BY observation_id, evidence_id, role
		)), md5(''))
	FROM stacks_core.observation_evidence
	ORDER BY 1`

type temporalPausingBeginner struct {
	beginner           temporalQueryBeginner
	firstAuthorityRead chan<- struct{}
	releaseReader      <-chan struct{}
}

func (beginner temporalPausingBeginner) BeginTx(
	ctx context.Context,
	options pgx.TxOptions,
) (pgx.Tx, error) {
	transaction, err := beginner.beginner.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &temporalPausingTransaction{
		Tx:                 transaction,
		ctx:                ctx,
		firstAuthorityRead: beginner.firstAuthorityRead,
		releaseReader:      beginner.releaseReader,
	}, nil
}

type temporalPausingTransaction struct {
	pgx.Tx
	ctx                context.Context
	queryCount         int
	firstAuthorityRead chan<- struct{}
	releaseReader      <-chan struct{}
}

func (transaction *temporalPausingTransaction) Query(
	ctx context.Context,
	sql string,
	arguments ...any,
) (pgx.Rows, error) {
	rows, err := transaction.Tx.Query(ctx, sql, arguments...)
	if err != nil {
		return nil, err
	}
	transaction.queryCount++
	if transaction.queryCount != 1 {
		return rows, nil
	}
	return &temporalPausingRows{
		Rows:               rows,
		ctx:                transaction.ctx,
		firstAuthorityRead: transaction.firstAuthorityRead,
		releaseReader:      transaction.releaseReader,
	}, nil
}

type temporalPausingRows struct {
	pgx.Rows
	ctx                context.Context
	once               sync.Once
	firstAuthorityRead chan<- struct{}
	releaseReader      <-chan struct{}
}

func (rows *temporalPausingRows) Close() {
	rows.Rows.Close()
	rows.once.Do(func() {
		close(rows.firstAuthorityRead)
		select {
		case <-rows.releaseReader:
		case <-rows.ctx.Done():
		}
	})
}

func mustTemporalSection(
	t testing.TB,
	input evidence.SectionInput,
) evidence.Section {
	t.Helper()
	value, err := evidence.NewSection(input)
	if err != nil {
		t.Fatalf("evidence.NewSection(%q) error = %v", input.ID, err)
	}
	return value
}

func mustTemporalDocument(
	t testing.TB,
	input evidence.DocumentVersionInput,
) evidence.DocumentVersion {
	t.Helper()
	value, err := evidence.NewDocumentVersion(input)
	if err != nil {
		t.Fatalf("evidence.NewDocumentVersion() error = %v", err)
	}
	return value
}

func mustTemporalSpan(
	t testing.TB,
	document evidence.DocumentVersion,
	section evidence.Section,
	startOffset int,
	endOffset int,
	quote string,
	recordedAt time.Time,
) evidence.EvidenceSpan {
	t.Helper()
	value, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
		Document:    document,
		SectionID:   section.ID(),
		StartOffset: startOffset,
		EndOffset:   endOffset,
		Quote:       quote,
		RecordedAt:  recordedAt,
	})
	if err != nil {
		t.Fatalf("evidence.NewEvidenceSpan(%q) error = %v", section.ID(), err)
	}
	return value
}

func mustTemporalEntity(
	t testing.TB,
	id identity.EntityID,
	displayName string,
) identity.Entity {
	t.Helper()
	value, err := identity.NewEntity(identity.EntityInput{
		ID:          id,
		Kind:        identity.KindPerson,
		DisplayName: displayName,
		RecordedAt:  atlasEntityRecordedAt,
	})
	if err != nil {
		t.Fatalf("identity.NewEntity(%q) error = %v", id, err)
	}
	return value
}

func mustTemporalMention(
	t testing.TB,
	input identity.MentionInput,
) identity.MentionRecord {
	t.Helper()
	value, err := identity.NewMention(input)
	if err != nil {
		t.Fatalf("identity.NewMention(%q) error = %v", input.ID, err)
	}
	return value
}

func mustTemporalProposal(
	t testing.TB,
	input identity.ResolutionProposalInput,
) identity.ResolutionProposal {
	t.Helper()
	value, err := identity.NewResolutionProposal(input)
	if err != nil {
		t.Fatalf("identity.NewResolutionProposal(%q) error = %v", input.ID, err)
	}
	return value
}

func mustTemporalResolutionDecision(
	t testing.TB,
	input identity.ResolutionDecisionInput,
) identity.ResolutionDecision {
	t.Helper()
	value, err := identity.NewResolutionDecision(input)
	if err != nil {
		t.Fatalf("identity.NewResolutionDecision(%q) error = %v", input.ID, err)
	}
	return value
}

func mustTemporalAdmissionDecision(
	t testing.TB,
	input admission.DecisionInput,
) admission.Decision {
	t.Helper()
	value, err := admission.NewDecision(input)
	if err != nil {
		t.Fatalf("admission.NewDecision(%q) error = %v", input.ID, err)
	}
	return value
}

func mustTemporalMentionTerm(
	t testing.TB,
	mentionID identity.MentionID,
) observation.Term {
	t.Helper()
	value, err := observation.NewMentionTerm(string(mentionID))
	if err != nil {
		t.Fatalf("observation.NewMentionTerm(%q) error = %v", mentionID, err)
	}
	return value
}

func mustTemporalEntityTerm(
	t testing.TB,
	entityID identity.EntityID,
	groundingMentionID identity.MentionID,
) observation.Term {
	t.Helper()
	value, err := observation.NewEntityTerm(
		string(entityID),
		string(groundingMentionID),
	)
	if err != nil {
		t.Fatalf("observation.NewEntityTerm(%q) error = %v", entityID, err)
	}
	return value
}

func mustTemporalInstant(
	t testing.TB,
	value time.Time,
) observation.TemporalExtent {
	t.Helper()
	result, err := observation.AtTime(value)
	if err != nil {
		t.Fatalf("observation.AtTime() error = %v", err)
	}
	return result
}

func mustTemporalObservation(
	t testing.TB,
	input observation.ObservationInput,
) observation.Observation {
	t.Helper()
	value, err := observation.NewObservation(input)
	if err != nil {
		t.Fatalf("observation.NewObservation(%q) error = %v", input.ID, err)
	}
	return value
}

func assertTemporalResolvedEntity(
	t testing.TB,
	term observation.Term,
	want identity.EntityID,
) {
	t.Helper()
	entityID, groundingMentionID, ok := term.Entity()
	if !ok || entityID != string(want) || groundingMentionID != "" {
		t.Fatalf(
			"resolved term = %#v, want ungrounded entity %q",
			term,
			want,
		)
	}
}

func assertTemporalCoverage(
	t testing.TB,
	records []TemporalCoverageRecord,
	wantReason TemporalCoverageReason,
	wantObservationID observation.ObservationID,
) {
	t.Helper()
	for _, record := range records {
		if record.Reason == wantReason &&
			record.ObservationID == wantObservationID {
			return
		}
	}
	t.Fatalf(
		"coverage = %#v, want reason %q for observation %q",
		records,
		wantReason,
		wantObservationID,
	)
}

func assertNoTemporalCoverageForObservation(
	t testing.TB,
	records []TemporalCoverageRecord,
	observationID observation.ObservationID,
) {
	t.Helper()
	for _, record := range records {
		if record.ObservationID == observationID {
			t.Fatalf(
				"coverage = %#v, want no exclusion for observation %q",
				records,
				observationID,
			)
		}
	}
}

func assertTemporalEvidenceIDsExactlyOnce(
	t testing.TB,
	records []TemporalEvidenceRecord,
	want ...evidence.EvidenceID,
) {
	t.Helper()
	if len(records) != len(want) {
		t.Fatalf("citation count = %d, want %d", len(records), len(want))
	}
	counts := make(map[evidence.EvidenceID]int, len(records))
	for _, record := range records {
		counts[record.EvidenceID]++
	}
	for _, evidenceID := range want {
		if counts[evidenceID] != 1 {
			t.Fatalf(
				"citation evidence ID %q occurred %d times, want exactly once",
				evidenceID,
				counts[evidenceID],
			)
		}
	}
}

func assertTemporalObservationEqual(
	t testing.TB,
	got observation.Observation,
	want observation.Observation,
) {
	t.Helper()
	if got.ID() != want.ID() ||
		got.Statement() != want.Statement() ||
		!reflect.DeepEqual(got.ValidTime(), want.ValidTime()) ||
		got.RecordedAt() != want.RecordedAt() ||
		!reflect.DeepEqual(got.EvidenceLinks(), want.EvidenceLinks()) ||
		got.Derivation() != want.Derivation() ||
		got.Status() != want.Status() ||
		got.DigestVersion() != want.DigestVersion() ||
		got.Digest() != want.Digest() {
		t.Fatalf(
			"canonical observation %q did not round-trip exactly",
			want.ID(),
		)
	}
	gotConfidence, gotHasConfidence := got.Confidence()
	wantConfidence, wantHasConfidence := want.Confidence()
	if gotHasConfidence != wantHasConfidence ||
		(gotHasConfidence && gotConfidence != wantConfidence) {
		t.Fatalf(
			"canonical observation %q confidence = (%#v, %v), want (%#v, %v)",
			want.ID(),
			gotConfidence,
			gotHasConfidence,
			wantConfidence,
			wantHasConfidence,
		)
	}
}
