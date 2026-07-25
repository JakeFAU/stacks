package drive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"stacks/internal/analysis"
	"stacks/internal/entity"
	"stacks/internal/extract"
	"stacks/internal/ingest"
	"stacks/internal/modelpolicy"
)

type chronologyDocument struct {
	id         string
	title      string
	transcript string
	modelDate  string
	direction  string
}

func TestDriveMeetingTitleDatesFlowThroughSyncIntoAnalysisChronology(t *testing.T) {
	documents := []chronologyDocument{
		{
			id: "meeting-earlier", title: "[2026-06-03] Synthetic weekly meeting",
			transcript: "Leader gave Report ownership of the follow-up.", modelDate: "2099-01-01",
			direction: extract.SignalDirectionStrengthening,
		},
		{
			id: "meeting-later", title: "[2026-07-08] Synthetic weekly meeting",
			transcript: "Leader corrected work assigned to Report.", modelDate: "2099-01-02",
			direction: extract.SignalDirectionWeakening,
		},
		{
			id: "deadline-only", title: "Synthetic deadline review",
			transcript: "Leader asked Report to finish by 2026-08-01.", modelDate: "2026-08-01",
			direction: extract.SignalDirectionStrengthening,
		},
		{
			id: "unused-date-only", title: "Synthetic archive review",
			transcript: "Archive label 2026-09-01. Leader briefed Report.", modelDate: "2026-09-01",
			direction: extract.SignalDirectionWeakening,
		},
	}

	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/files":
			return jsonResponse(request, driveListResponse(documents)), nil
		default:
			const prefix = "/v1/documents/"
			if !strings.HasPrefix(request.URL.Path, prefix) {
				return nil, errors.New("unexpected synthetic provider request")
			}
			documentID := strings.TrimPrefix(request.URL.Path, prefix)
			for _, document := range documents {
				if document.id == documentID {
					return jsonResponse(request, docsGetResponse(document.id, document.title, document.transcript)), nil
				}
			}
			return nil, errors.New("unexpected synthetic document ID")
		}
	})}
	sourceBoundary := newTestClient(t, httpClient, NewTabClassifier([]string{"Transcript"}, nil))
	repository := &chronologyRepository{completions: make(map[string]ingest.Completion)}
	model := &chronologyModel{outputs: make([]extract.ExtractionOutput, len(documents))}
	for index, document := range documents {
		model.outputs[index] = chronologyExtraction(document.transcript, document.modelDate, document.direction)
	}
	service := &ingest.Service{
		Source: sourceBoundary, Model: model, Resolver: entity.Resolver{}, Repository: repository,
		CollectionID: "synthetic-folder", PromptVersion: extract.ExtractionPromptVersion,
		Provider: modelpolicy.ProviderBedrock, DataMode: modelpolicy.DataModePersonal,
		Region: "us-east-1", ModelID: "synthetic-model", MaxTokens: 256,
		LeaseDuration: 5 * time.Minute, AttemptTimeout: 4 * time.Minute,
		Now: func() time.Time { return time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC) },
	}

	summary, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if summary.Completed != len(documents) || model.calls != len(documents) {
		t.Fatalf("sync summary/model calls = %#v/%d, want four completed source documents", summary, model.calls)
	}

	var dated, contentDated []analysis.Signal
	for index, result := range summary.Results {
		completion := repository.completions[result.VersionID]
		if len(completion.Observations) != 1 || len(completion.Signals) != 1 {
			t.Fatalf("completion %d = %#v, want one observation and signal", index, completion)
		}
		observation := completion.Observations[0]
		signal := completion.Signals[0]
		converted := analysis.Signal{
			ID: signal.ID, MeetingID: result.DocumentID,
			Category: analysis.Category(signal.Category), Direction: analysis.Direction(signal.Direction),
			ValidTime: observation.ValidStart, Validated: true, TranscriptBacked: true,
		}
		if index < 2 {
			dated = append(dated, converted)
		} else {
			contentDated = append(contentDated, converted)
		}
	}

	if got := analysis.AdmitConclusion(analysis.AdmissionInput{
		PairAccepted: true, Proposed: analysis.StatusPossibleDecline, Signals: dated,
		SupportingSignalIDs: []string{dated[0].ID, dated[1].ID},
	}); got != analysis.StatusPossibleDecline {
		t.Fatalf("dated source-title admission = %q, want %q", got, analysis.StatusPossibleDecline)
	}
	wantEarlier := time.Date(2026, time.June, 3, 0, 0, 0, 0, time.UTC)
	wantLater := time.Date(2026, time.July, 8, 0, 0, 0, 0, time.UTC)
	if dated[0].ValidTime == nil || !dated[0].ValidTime.Equal(wantEarlier) ||
		dated[1].ValidTime == nil || !dated[1].ValidTime.Equal(wantLater) {
		t.Fatalf("dated source times = %v/%v, want title-derived %v/%v", dated[0].ValidTime, dated[1].ValidTime, wantEarlier, wantLater)
	}
	if got := analysis.AdmitConclusion(analysis.AdmissionInput{
		PairAccepted: true, Proposed: analysis.StatusPossibleDecline, Signals: contentDated,
		SupportingSignalIDs: []string{contentDated[0].ID, contentDated[1].ID},
	}); got != analysis.StatusInsufficientEvidence {
		t.Fatalf("deadline/unused-date admission = %q, want no chronology from arbitrary content dates", got)
	}
	for index, signal := range contentDated {
		if signal.ValidTime != nil {
			t.Fatalf("content-dated signal %d valid time = %v, want unknown", index, signal.ValidTime)
		}
	}
}

func driveListResponse(documents []chronologyDocument) string {
	files := make([]map[string]any, len(documents))
	for index, document := range documents {
		files[index] = map[string]any{
			"id": document.id, "name": document.title, "version": fmt.Sprintf("%d", index+1),
			"modifiedTime": "2026-07-22T11:00:00Z",
		}
	}
	encoded, _ := json.Marshal(map[string]any{"files": files})
	return string(encoded)
}

func docsGetResponse(documentID, title, transcript string) string {
	encoded, _ := json.Marshal(map[string]any{
		"documentId": documentID, "title": title, "revisionId": "synthetic-revision",
		"tabs": []any{map[string]any{
			"tabProperties": map[string]any{"tabId": "transcript-tab", "title": "Transcript"},
			"documentTab": map[string]any{"body": map[string]any{"content": []any{map[string]any{
				"paragraph": map[string]any{"elements": []any{map[string]any{
					"textRun": map[string]any{"content": transcript},
				}}},
			}}}},
		}},
	})
	return string(encoded)
}

func chronologyExtraction(transcript, modelDate, direction string) extract.ExtractionOutput {
	return extract.ExtractionOutput{
		MeetingDate: modelDate,
		Citations: []extract.Citation{{
			ID: "citation", TabID: "transcript-tab", StartOffset: 0, EndOffset: len(transcript), Quote: transcript,
		}},
		People: []extract.PersonMention{
			{ID: "leader", Surface: "Leader", Role: extract.MentionRoleSpeaker, CitationIDs: []string{"citation"}},
			{ID: "report", Surface: "Report", Role: extract.MentionRoleReference, CitationIDs: []string{"citation"}},
		},
		Statements: []extract.AttributedStatement{{
			ID: "statement", SpeakerMentionID: "leader", SubjectMentionID: "report",
			Predicate: "interacted", ObjectText: "synthetic work", ValidDate: modelDate, CitationIDs: []string{"citation"},
		}},
		Signals: []extract.InteractionSignal{{
			ID: "signal", SubjectMentionID: "leader", ObjectMentionID: "report", StatementIDs: []string{"statement"},
			Category: extract.SignalCategoryScrutinyCorrection, Direction: direction,
			Rationale: "Synthetic observable interaction.", Confidence: 0.8,
			SupportingCitationIDs: []string{"citation"}, ContradictingCitationIDs: []string{},
		}},
	}
}

type chronologyModel struct {
	outputs []extract.ExtractionOutput
	calls   int
}

func (model *chronologyModel) Generate(_ context.Context, request extract.Request) (extract.Response, error) {
	if model.calls >= len(model.outputs) {
		return extract.Response{}, errors.New("unexpected synthetic model call")
	}
	output, err := json.Marshal(model.outputs[model.calls])
	model.calls++
	if err != nil {
		return extract.Response{}, err
	}
	return extract.Response{
		Output: output, ModelID: "synthetic-model", PromptVersion: request.PromptVersion, Outcome: "success",
	}, nil
}

type chronologyRepository struct {
	completions map[string]ingest.Completion
}

func (repository *chronologyRepository) PrepareVersion(
	_ context.Context,
	version evidence.DocumentVersion,
	derivation ingest.DerivationIdentity,
	_ modelpolicy.DataMode,
	leaseDuration time.Duration,
) (ingest.VersionState, error) {
	return ingest.VersionState{
		ID: "version-" + version.ProviderDocumentID(), DerivationID: "derivation-" + version.ProviderDocumentID(),
		DerivationDigest: derivation.Digest, LeaseOwner: "owner-" + version.ProviderDocumentID(),
		LeaseExpiresAt: time.Now().Add(leaseDuration), Status: ingest.VersionStatusPending,
	}, nil
}

func (repository *chronologyRepository) CompleteVersion(_ context.Context, completion ingest.Completion) error {
	repository.completions[completion.VersionID] = completion
	return nil
}

func (repository *chronologyRepository) RecordFailure(_ context.Context, derivationID, _ string, _ ingest.VersionStatus, code ingest.FailureCode) error {
	return fmt.Errorf("unexpected failure for %s: %s", derivationID, code)
}

func (repository *chronologyRepository) EntitySnapshots(context.Context) ([]entity.EntitySnapshot, error) {
	return nil, nil
}
