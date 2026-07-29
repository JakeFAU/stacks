package ingest

import (
	"crypto/sha256"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"

	"stacks/internal/entity"
	"stacks/internal/extract"
)

func TestIngestionBuildsCanonicalInteractionObservationsWithoutAnalysisImport(t *testing.T) {
	completion := canonicalInteractionCompletion(t, "2026-07-25")

	if len(completion.Observations) != 1 {
		t.Fatalf("completion observation count = %d, want 1", len(completion.Observations))
	}
	predicate := completion.Observations[0].Predicate
	if predicate != "stacks.interaction.v1/future_responsibility/strengthening" {
		t.Fatalf("canonical predicate = %q", predicate)
	}

	command := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, ".")
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list ingestion production imports: %v", err)
	}
	analysisImportPath := "stacks/" + strings.ReplaceAll("internal/anXalysis", "X", "")
	for _, imported := range strings.Fields(string(output)) {
		if imported == analysisImportPath {
			t.Fatalf("ingestion production import = %q, want extraction-owned mapping boundary", imported)
		}
	}
}

func TestInteractionDraftPersistsGroundedMentionTerms(t *testing.T) {
	completion := canonicalInteractionCompletion(t, "2026-07-25")
	draft := completion.Observations[0]

	if draft.Subject.Kind != observation.TermMention || draft.Subject.MentionKey != "mention-leader" {
		t.Fatalf("subject draft = %#v, want source-grounded leader mention", draft.Subject)
	}
	if draft.Object.Kind != observation.TermMention || draft.Object.MentionKey != "mention-report" {
		t.Fatalf("object draft = %#v, want source-grounded report mention", draft.Object)
	}
	if draft.Subject.EntityID != "" || draft.Object.EntityID != "" {
		t.Fatalf("interaction terms froze extraction-time entities: subject=%#v object=%#v", draft.Subject, draft.Object)
	}
}

func TestCanonicalDraftKeepsSupportingAndContradictingRoles(t *testing.T) {
	completion := canonicalInteractionCompletion(t, "2026-07-25")
	got := completion.Observations[0].Evidence
	want := []DraftEvidenceLink{
		{EvidenceKey: "citation-supporting", Role: observation.EvidenceSupporting},
		{EvidenceKey: "citation-contradicting", Role: observation.EvidenceContradicting},
	}
	if len(got) != len(want) {
		t.Fatalf("draft evidence = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("draft evidence[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func TestCanonicalDraftUsesInstantOrUnknownSourceTime(t *testing.T) {
	t.Run("instant", func(t *testing.T) {
		draft := canonicalInteractionCompletion(t, "2026-07-25").Observations[0]
		instant, ok := draft.ValidTime.Instant()
		want := time.Date(2026, time.July, 25, 0, 0, 0, 0, time.UTC)
		if draft.ValidTime.Kind() != observation.TemporalInstant || !ok || instant != want {
			t.Fatalf("draft valid time = %#v, want instant %v", draft.ValidTime, want)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		draft := canonicalInteractionCompletion(t, "").Observations[0]
		if draft.ValidTime.Kind() != observation.TemporalUnknown {
			t.Fatalf("draft valid time kind = %v, want unknown", draft.ValidTime.Kind())
		}
	})
}

func TestCanonicalCompletionBuildsIdentityInputsAndInitialAdmission(t *testing.T) {
	completion := canonicalInteractionCompletionWithSnapshots(
		t,
		"2026-07-25",
		[]entity.EntitySnapshot{{
			ID: "entity-leader", Kind: identity.KindPerson, DisplayName: "Leader",
			RecordedAt: validationRecordedAt,
		}},
	)

	if len(completion.Proposals) != 2 {
		t.Fatalf("resolution proposal count = %d, want 2", len(completion.Proposals))
	}
	if len(completion.Candidates) != 1 ||
		completion.Candidates[0].EntityID() != "entity-leader" ||
		completion.Candidates[0].Rank() != 1 {
		t.Fatalf("resolution candidates = %#v, want ranked leader candidate", completion.Candidates)
	}
	wantTargets := map[admission.TargetKind]int{
		admission.TargetExtractionRun: 1,
		admission.TargetMention:       2,
		admission.TargetObservation:   1,
	}
	gotTargets := make(map[admission.TargetKind]int)
	for _, decision := range completion.AdmissionDecisions {
		if decision.Outcome() != admission.Admitted ||
			decision.Authority() != admission.AuthorityPolicy {
			t.Fatalf("initial admission = %#v, want policy admission", decision)
		}
		gotTargets[decision.TargetKind()]++
	}
	for kind, want := range wantTargets {
		if gotTargets[kind] != want {
			t.Fatalf("admission target %q count = %d, want %d", kind, gotTargets[kind], want)
		}
	}
}

func canonicalInteractionCompletion(t *testing.T, meetingDate string) Completion {
	t.Helper()
	return canonicalInteractionCompletionWithSnapshots(t, meetingDate, nil)
}

func canonicalInteractionCompletionWithSnapshots(
	t *testing.T,
	meetingDate string,
	snapshots []entity.EntitySnapshot,
) Completion {
	t.Helper()
	const transcript = "Leader assigns follow-up. Report notes a competing constraint."
	document := syntheticDocument("canonical-interaction", transcript)
	if meetingDate != "" {
		sourceTime, err := time.Parse(time.DateOnly, meetingDate)
		if err != nil {
			t.Fatalf("parse synthetic source meeting time: %v", err)
		}
		document.MeetingTime = &sourceTime
	}
	version := documentVersion(t, document)
	recordedAt := time.Date(2026, time.July, 25, 15, 4, 3, 123456000, time.UTC)
	output := extract.ExtractionOutput{
		MeetingDate: meetingDate,
		Citations: []extract.Citation{
			{
				ID: "citation-supporting", TabID: "transcript-tab", StartOffset: 0,
				EndOffset: len("Leader assigns follow-up."), Quote: "Leader assigns follow-up.",
			},
			{
				ID: "citation-contradicting", TabID: "transcript-tab", StartOffset: len("Leader assigns follow-up. "),
				EndOffset: len(transcript), Quote: "Report notes a competing constraint.",
			},
		},
		People: []extract.PersonMention{
			{
				ID: "mention-leader", Surface: "Leader", Role: extract.MentionRoleSpeaker,
				CitationIDs: []string{"citation-supporting"},
			},
			{
				ID: "mention-report", Surface: "Report", Role: extract.MentionRoleReference,
				CitationIDs: []string{"citation-contradicting"},
			},
		},
		Statements: []extract.AttributedStatement{{
			ID: "statement-1", SpeakerMentionID: "mention-leader", SubjectMentionID: "mention-report",
			Predicate: "assigned", ObjectText: "follow-up", ValidDate: meetingDate,
			CitationIDs: []string{"citation-supporting"},
		}},
		Signals: []extract.InteractionSignal{{
			ID: "signal-1", SubjectMentionID: "mention-leader", ObjectMentionID: "mention-report",
			StatementIDs: []string{"statement-1"}, Category: extract.SignalCategoryFutureResponsibility,
			Direction: extract.SignalDirectionStrengthening, Rationale: "model-authored and not durable",
			Confidence: 0.8, SupportingCitationIDs: []string{"citation-supporting"},
			ContradictingCitationIDs: []string{"citation-contradicting"},
		}},
	}
	service := &Service{Resolver: entity.Resolver{}, DataMode: "personal", Now: func() time.Time { return recordedAt }}
	completion, err := service.completion(version, VersionState{
		VersionID: "version-id", RunID: "run-id", AttemptID: "attempt-id", LeaseOwner: "lease-owner",
		DocumentRecordedAt: version.RecordedAt(),
	}, DerivationIdentity{
		ModelID: "synthetic-model", PromptVersion: extract.ExtractionPromptVersion,
		Digest: sha256.Sum256([]byte("synthetic derivation")),
	}, extract.Response{
		ModelID: "synthetic-model", PromptVersion: extract.ExtractionPromptVersion,
	}, output, snapshots)
	if err != nil {
		t.Fatalf("completion() error = %v", err)
	}
	return completion
}
