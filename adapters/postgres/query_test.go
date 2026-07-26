package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
)

func TestObservationQueryResolvesCurrentIdentityAndAdmission(t *testing.T) {
	fixture := newObservationRepositoryFixture(t)
	firstEntity := canonicalEntity(t, "entity:opaque/relationship-a", "Synthetic Contributor A")
	secondEntity := canonicalEntity(t, "entity:opaque/relationship-b", "Synthetic Contributor B")
	correctedEntity := canonicalEntity(t, "entity:opaque/relationship-c", "Synthetic Contributor C")
	firstMention := canonicalMention(
		t,
		"mention:opaque/relationship-a",
		fixture.evidence[0],
		"Synthetic Contributor A",
		"",
	)
	secondMention := canonicalMention(
		t,
		"mention:opaque/relationship-b",
		fixture.evidence[1],
		"Synthetic Contributor B",
		"",
	)
	firstProposal := canonicalProposal(
		t,
		"proposal:opaque/relationship-a",
		firstMention.ID(),
		fixture.evidence[0],
	)
	secondProposal := canonicalProposal(
		t,
		"proposal:opaque/relationship-b",
		secondMention.ID(),
		fixture.evidence[1],
	)
	initialFirstDecision := canonicalResolutionDecision(
		t,
		"decision:opaque/relationship-a-initial",
		firstProposal.ID(),
		identity.DecisionAccepted,
		firstEntity.ID(),
		identity.AuthorityReviewer,
		"",
		observationRecordedAt.Add(time.Minute),
	)
	correctedFirstDecision := canonicalResolutionDecision(
		t,
		"decision:opaque/relationship-a-corrected",
		firstProposal.ID(),
		identity.DecisionAccepted,
		correctedEntity.ID(),
		identity.AuthorityReviewer,
		initialFirstDecision.ID(),
		observationRecordedAt.Add(2*time.Minute),
	)
	secondDecision := canonicalResolutionDecision(
		t,
		"decision:opaque/relationship-b",
		secondProposal.ID(),
		identity.DecisionAccepted,
		secondEntity.ID(),
		identity.AuthorityReviewer,
		"",
		observationRecordedAt.Add(time.Minute),
	)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		for _, entity := range []identity.Entity{firstEntity, secondEntity, correctedEntity} {
			if _, err := transaction.PutEntity(fixture.ctx, entity); err != nil {
				return err
			}
		}
		for _, mention := range []identity.MentionRecord{firstMention, secondMention} {
			if _, err := transaction.PutMention(fixture.ctx, mention); err != nil {
				return err
			}
		}
		for _, proposal := range []identity.ResolutionProposal{firstProposal, secondProposal} {
			if _, err := transaction.PutResolutionProposal(fixture.ctx, proposal); err != nil {
				return err
			}
		}
		if err := transaction.AppendResolutionDecision(
			fixture.ctx,
			initialFirstDecision,
			nil,
		); err != nil {
			return err
		}
		if err := transaction.AppendResolutionDecision(
			fixture.ctx,
			correctedFirstDecision,
			nil,
		); err != nil {
			return err
		}
		if err := transaction.AppendResolutionDecision(
			fixture.ctx,
			secondDecision,
			nil,
		); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("persist relationship identity authority: %v", err)
	}

	firstMentionTerm, err := observation.NewMentionTerm(string(firstMention.ID()))
	if err != nil {
		t.Fatalf("observation.NewMentionTerm(first) error = %v", err)
	}
	secondMentionTerm, err := observation.NewMentionTerm(string(secondMention.ID()))
	if err != nil {
		t.Fatalf("observation.NewMentionTerm(second) error = %v", err)
	}
	firstEntityTerm, err := observation.NewEntityTerm(string(firstEntity.ID()), "")
	if err != nil {
		t.Fatalf("observation.NewEntityTerm(first) error = %v", err)
	}
	secondEntityTerm, err := observation.NewEntityTerm(
		string(secondEntity.ID()),
		string(secondMention.ID()),
	)
	if err != nil {
		t.Fatalf("observation.NewEntityTerm(second) error = %v", err)
	}
	direct := canonicalObservation(
		t,
		"observation:relationship/direct",
		firstEntityTerm,
		"collaboration.supports",
		secondMentionTerm,
		observation.UnknownTime(),
		observationRecordedAt.Add(3*time.Minute),
		observation.StatusObserved,
		[]observation.EvidenceLink{supportingLink(fixture.evidence[0])},
		nil,
		fixture.run.ID,
	)
	corrected := canonicalObservation(
		t,
		"observation:relationship/corrected",
		firstMentionTerm,
		"collaboration.supports",
		secondEntityTerm,
		observation.UnknownTime(),
		observationRecordedAt.Add(4*time.Minute),
		observation.StatusObserved,
		[]observation.EvidenceLink{
			supportingLink(fixture.evidence[1]),
			{
				EvidenceID: fixture.evidence[0].ID(),
				Role:       observation.EvidenceContradicting,
			},
		},
		nil,
		fixture.run.ID,
	)
	retired := canonicalObservation(
		t,
		"observation:relationship/retired",
		firstEntityTerm,
		"collaboration.supports",
		secondEntityTerm,
		observation.UnknownTime(),
		observationRecordedAt.Add(5*time.Minute),
		observation.StatusObserved,
		[]observation.EvidenceLink{supportingLink(fixture.evidence[0])},
		nil,
		fixture.run.ID,
	)
	quarantined := canonicalObservation(
		t,
		"observation:relationship/quarantined",
		firstEntityTerm,
		"collaboration.supports",
		secondEntityTerm,
		observation.UnknownTime(),
		observationRecordedAt.Add(6*time.Minute),
		observation.StatusObserved,
		[]observation.EvidenceLink{supportingLink(fixture.evidence[1])},
		nil,
		fixture.run.ID,
	)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		for _, value := range []observation.Observation{direct, corrected, retired, quarantined} {
			if _, err := transaction.PutObservation(fixture.ctx, value); err != nil {
				return err
			}
		}
		decisions := []admission.Decision{
			canonicalAdmissionDecision(
				t,
				"admission:relationship/direct",
				admission.TargetObservation,
				string(direct.ID()),
				admission.Admitted,
				admission.AuthorityPolicy,
				"",
				observationRecordedAt.Add(7*time.Minute),
			),
			canonicalAdmissionDecision(
				t,
				"admission:relationship/corrected",
				admission.TargetObservation,
				string(corrected.ID()),
				admission.Admitted,
				admission.AuthorityPolicy,
				"",
				observationRecordedAt.Add(7*time.Minute),
			),
			canonicalAdmissionDecision(
				t,
				"admission:relationship/retired-initial",
				admission.TargetObservation,
				string(retired.ID()),
				admission.Admitted,
				admission.AuthorityPolicy,
				"",
				observationRecordedAt.Add(7*time.Minute),
			),
			canonicalAdmissionDecision(
				t,
				"admission:relationship/quarantined",
				admission.TargetObservation,
				string(quarantined.ID()),
				admission.Quarantined,
				admission.AuthorityPolicy,
				"",
				observationRecordedAt.Add(7*time.Minute),
			),
		}
		for _, decision := range decisions {
			if err := transaction.AppendAdmissionDecision(fixture.ctx, decision); err != nil {
				return err
			}
		}
		return transaction.AppendAdmissionDecision(
			fixture.ctx,
			canonicalAdmissionDecision(
				t,
				"admission:relationship/retired-current",
				admission.TargetObservation,
				string(retired.ID()),
				admission.Retired,
				admission.AuthorityReviewer,
				"admission:relationship/retired-initial",
				observationRecordedAt.Add(8*time.Minute),
			),
		)
	}); err != nil {
		t.Fatalf("persist relationship observations and admission: %v", err)
	}

	directRecords, err := fixture.database.ListAdmittedRelationshipObservations(
		fixture.ctx,
		firstEntity.ID(),
		secondEntity.ID(),
	)
	if err != nil {
		t.Fatalf("ListAdmittedRelationshipObservations(direct pair) error = %v", err)
	}
	if len(directRecords) != 1 ||
		directRecords[0].Observation.ID() != direct.ID() ||
		directRecords[0].SubjectEntityID != firstEntity.ID() ||
		directRecords[0].ObjectEntityID != secondEntity.ID() {
		t.Fatalf("direct pair records = %#v, want only direct admitted observation", directRecords)
	}
	directSubjectEntityID, directGroundingID, ok := directRecords[0].Observation.Statement().Subject.Entity()
	if !ok ||
		directSubjectEntityID != string(firstEntity.ID()) ||
		directGroundingID != "" {
		t.Fatalf(
			"direct assertion subject = (%q, %q, %v), want original direct entity",
			directSubjectEntityID,
			directGroundingID,
			ok,
		)
	}

	correctedRecords, err := fixture.database.ListAdmittedRelationshipObservations(
		fixture.ctx,
		correctedEntity.ID(),
		secondEntity.ID(),
	)
	if err != nil {
		t.Fatalf("ListAdmittedRelationshipObservations(corrected pair) error = %v", err)
	}
	if len(correctedRecords) != 1 ||
		correctedRecords[0].Observation.ID() != corrected.ID() ||
		correctedRecords[0].EffectiveAdmission.Outcome() != admission.Admitted {
		t.Fatalf("corrected pair records = %#v, want only current-authority observation", correctedRecords)
	}
	if len(correctedRecords[0].Evidence) != 2 {
		t.Fatalf("corrected observation evidence count = %d, want 2", len(correctedRecords[0].Evidence))
	}
	gotRoles := make(map[string]observation.EvidenceRole, 2)
	for index, record := range correctedRecords[0].Evidence {
		gotRoles[string(record.Span.ID())] = record.Role
		if index > 0 {
			previous := correctedRecords[0].Evidence[index-1]
			if previous.Span.ID() > record.Span.ID() ||
				(previous.Span.ID() == record.Span.ID() && previous.Role > record.Role) {
				t.Fatalf(
					"corrected evidence is not in canonical ID-role order: %#v",
					correctedRecords[0].Evidence,
				)
			}
		}
	}
	wantRoles := map[string]observation.EvidenceRole{
		string(fixture.evidence[0].ID()): observation.EvidenceContradicting,
		string(fixture.evidence[1].ID()): observation.EvidenceSupporting,
	}
	if !reflect.DeepEqual(gotRoles, wantRoles) {
		t.Fatalf("corrected evidence roles = %v, want %v", gotRoles, wantRoles)
	}
	for _, record := range correctedRecords[0].Evidence {
		if record.Span.ID() == "" ||
			record.SourceDocumentID == "" ||
			record.DocumentVersionID == "" ||
			record.SectionID == "" ||
			record.SectionTitle == "" ||
			record.SectionRole == "" {
			t.Fatalf("incomplete generic evidence provenance = %#v", record)
		}
	}
}

func TestRelationshipSnapshotUsesCurrentAcceptedAuthorityAndCorrectionWithoutIdentityAdmission(t *testing.T) {
	fixture := newObservationRepositoryFixture(t)
	manager := canonicalEntity(
		t,
		"entity:opaque/snapshot-manager",
		"Synthetic Manager",
	)
	employee := canonicalEntity(
		t,
		"entity:opaque/snapshot-employee",
		"Synthetic Employee",
	)
	correctedManager := canonicalEntity(
		t,
		"entity:opaque/snapshot-corrected-manager",
		"Synthetic Corrected Manager",
	)
	managerMention := canonicalMention(
		t,
		"mention:opaque/snapshot-manager",
		fixture.evidence[0],
		"Synthetic Manager",
		"",
	)
	employeeMention := canonicalMention(
		t,
		"mention:opaque/snapshot-employee",
		fixture.evidence[1],
		"Synthetic Employee",
		"",
	)
	managerProposal := canonicalProposal(
		t,
		"proposal:opaque/snapshot-manager",
		managerMention.ID(),
		fixture.evidence[0],
	)
	employeeProposal := canonicalProposal(
		t,
		"proposal:opaque/snapshot-employee",
		employeeMention.ID(),
		fixture.evidence[1],
	)
	managerDecision := canonicalResolutionDecision(
		t,
		"decision:opaque/snapshot-manager",
		managerProposal.ID(),
		identity.DecisionAccepted,
		manager.ID(),
		identity.AuthorityReviewer,
		"",
		observationRecordedAt.Add(time.Minute),
	)
	employeeDecision := canonicalResolutionDecision(
		t,
		"decision:opaque/snapshot-employee",
		employeeProposal.ID(),
		identity.DecisionAccepted,
		employee.ID(),
		identity.AuthorityReviewer,
		"",
		observationRecordedAt.Add(time.Minute),
	)
	if err := fixture.database.InTransaction(
		fixture.ctx,
		func(transaction *postgres.Transaction) error {
			for _, entity := range []identity.Entity{
				manager,
				employee,
				correctedManager,
			} {
				if _, err := transaction.PutEntity(fixture.ctx, entity); err != nil {
					return err
				}
			}
			for _, mention := range []identity.MentionRecord{
				managerMention,
				employeeMention,
			} {
				if _, err := transaction.PutMention(fixture.ctx, mention); err != nil {
					return err
				}
			}
			for _, proposal := range []identity.ResolutionProposal{
				managerProposal,
				employeeProposal,
			} {
				if _, err := transaction.PutResolutionProposal(
					fixture.ctx,
					proposal,
				); err != nil {
					return err
				}
			}
			for _, decision := range []identity.ResolutionDecision{
				managerDecision,
				employeeDecision,
			} {
				if err := transaction.AppendResolutionDecision(
					fixture.ctx,
					decision,
					nil,
				); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		t.Fatalf("persist snapshot identity authority: %v", err)
	}

	managerTerm, err := observation.NewMentionTerm(string(managerMention.ID()))
	if err != nil {
		t.Fatalf("observation.NewMentionTerm(manager) error = %v", err)
	}
	employeeTerm, err := observation.NewMentionTerm(string(employeeMention.ID()))
	if err != nil {
		t.Fatalf("observation.NewMentionTerm(employee) error = %v", err)
	}
	value := canonicalObservation(
		t,
		"observation:opaque/relationship-snapshot",
		managerTerm,
		"collaboration.supports",
		employeeTerm,
		observation.UnknownTime(),
		observationRecordedAt.Add(3*time.Minute),
		observation.StatusObserved,
		[]observation.EvidenceLink{supportingLink(fixture.evidence[0])},
		nil,
		fixture.run.ID,
	)
	if err := fixture.database.InTransaction(
		fixture.ctx,
		func(transaction *postgres.Transaction) error {
			if _, err := transaction.PutObservation(fixture.ctx, value); err != nil {
				return err
			}
			return transaction.AppendAdmissionDecision(
				fixture.ctx,
				canonicalAdmissionDecision(
					t,
					"admission:opaque/relationship-snapshot",
					admission.TargetObservation,
					string(value.ID()),
					admission.Admitted,
					admission.AuthorityPolicy,
					"",
					observationRecordedAt.Add(4*time.Minute),
				),
			)
		},
	); err != nil {
		t.Fatalf("persist admitted relationship observation: %v", err)
	}

	snapshot, err := fixture.database.LoadRelationshipSnapshot(
		fixture.ctx,
		manager.ID(),
		employee.ID(),
	)
	if err != nil {
		t.Fatalf("LoadRelationshipSnapshot() error = %v", err)
	}
	if !snapshot.SubjectAccepted ||
		!snapshot.ObjectAccepted ||
		len(snapshot.Observations) != 1 ||
		snapshot.Observations[0].Observation.ID() != value.ID() {
		t.Fatalf("initial snapshot = %#v", snapshot)
	}

	correction := canonicalResolutionDecision(
		t,
		"decision:opaque/snapshot-manager-correction",
		managerProposal.ID(),
		identity.DecisionAccepted,
		correctedManager.ID(),
		identity.AuthorityReviewer,
		managerDecision.ID(),
		observationRecordedAt.Add(5*time.Minute),
	)
	if err := fixture.database.InTransaction(
		fixture.ctx,
		func(transaction *postgres.Transaction) error {
			return transaction.AppendResolutionDecision(
				fixture.ctx,
				correction,
				nil,
			)
		},
	); err != nil {
		t.Fatalf("persist identity correction: %v", err)
	}

	original, err := fixture.database.LoadRelationshipSnapshot(
		fixture.ctx,
		manager.ID(),
		employee.ID(),
	)
	if err != nil {
		t.Fatalf("LoadRelationshipSnapshot(original after correction) error = %v", err)
	}
	if original.SubjectAccepted || !original.ObjectAccepted ||
		len(original.Observations) != 0 {
		t.Fatalf("original snapshot after correction = %#v", original)
	}
	corrected, err := fixture.database.LoadRelationshipSnapshot(
		fixture.ctx,
		correctedManager.ID(),
		employee.ID(),
	)
	if err != nil {
		t.Fatalf("LoadRelationshipSnapshot(corrected pair) error = %v", err)
	}
	if !corrected.SubjectAccepted ||
		!corrected.ObjectAccepted ||
		len(corrected.Observations) != 1 ||
		corrected.Observations[0].Observation.ID() != value.ID() {
		t.Fatalf("corrected snapshot = %#v", corrected)
	}
}

func TestRelationshipSnapshotPreservesCancellation(t *testing.T) {
	fixture := newObservationRepositoryFixture(t)
	ctx, cancel := context.WithCancel(fixture.ctx)
	cancel()

	_, err := fixture.database.LoadRelationshipSnapshot(
		ctx,
		"entity:opaque/canceled-subject",
		"entity:opaque/canceled-object",
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadRelationshipSnapshot() error = %v, want context.Canceled", err)
	}
}
