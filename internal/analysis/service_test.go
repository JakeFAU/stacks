package analysis

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"stacks/internal/extract"
	"stacks/internal/observability"
)

func TestServiceRequiresAcceptedConfiguredPair(t *testing.T) {
	repository := &fakeRepository{snapshot: PairSnapshot{Accepted: false}}
	model := &fakeModel{}
	service := testService(repository, model)

	_, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if !errors.Is(err, ErrPairNotAccepted) {
		t.Fatalf("Analyze() error = %v, want ErrPairNotAccepted", err)
	}
	if model.calls != 0 || repository.completeCalls != 0 {
		t.Fatalf("model/complete calls = %d/%d, want 0/0", model.calls, repository.completeCalls)
	}
}

func TestServiceReturnsInsufficientWithoutModelForFewerThanTwoDatedMeetings(t *testing.T) {
	snapshot := acceptedPairSnapshot()
	snapshot.Signals = snapshot.Signals[:1]
	repository := &fakeRepository{snapshot: snapshot}
	model := &fakeModel{}
	service := testService(repository, model)

	report, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Status != StatusInsufficientEvidence || model.calls != 0 {
		t.Fatalf("report status/model calls = %q/%d, want insufficient/0", report.Status, model.calls)
	}
	if repository.completeCalls != 1 {
		t.Fatalf("complete calls = %d, want 1 durable deterministic report", repository.completeCalls)
	}
}

func TestServiceTreatsTwoRevisionsOfOneSourceDocumentAsOneMeeting(t *testing.T) {
	snapshot := acceptedPairSnapshot()
	snapshot.Signals[1].MeetingID = snapshot.Signals[0].MeetingID
	repository := &fakeRepository{snapshot: snapshot}
	model := &fakeModel{}
	service := testService(repository, model)

	report, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Status != StatusInsufficientEvidence || model.calls != 0 {
		t.Fatalf("report/model = %q/%d, want insufficient/0 for one stable meeting", report.Status, model.calls)
	}
}

func TestServicePersistsAndCachesAcceptedPairWithNoEligibleSignals(t *testing.T) {
	repository := &fakeRepository{snapshot: PairSnapshot{Accepted: true}}
	model := &fakeModel{}
	service := testService(repository, model)

	first, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("first Analyze() error = %v", err)
	}
	if first.Status != StatusInsufficientEvidence || model.calls != 0 || repository.completeCalls != 1 {
		t.Fatalf("first report/model/complete = %q/%d/%d, want insufficient/0/1", first.Status, model.calls, repository.completeCalls)
	}
	repository.cached = first
	second, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("second Analyze() error = %v", err)
	}
	if second.ID != first.ID || model.calls != 0 || repository.completeCalls != 1 {
		t.Fatalf("cached report/model/complete = %q/%d/%d, want same/0/1", second.ID, model.calls, repository.completeCalls)
	}

	repository.snapshot = acceptedPairSnapshot()
	repository.cached = Report{}
	model.output = analysisOutput(StatusNoMaterialChange, []string{"signal-earlier"}, nil)
	third, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("third Analyze() error = %v", err)
	}
	if third.InputDigest == first.InputDigest || model.calls != 1 {
		t.Fatalf("accepted signals digest/model calls = %x/%d, want new identity and one model call", third.InputDigest, model.calls)
	}
}

func TestServiceRecordsInsufficientEvidenceDecisionWithoutModel(t *testing.T) {
	snapshot := acceptedPairSnapshot()
	snapshot.Signals = snapshot.Signals[:1]
	repository := &fakeRepository{snapshot: snapshot}
	decisions := &fakeDecisionRecorder{}
	service := testService(repository, &fakeModel{})
	service.Decisions = decisions

	if _, err := service.Analyze(context.Background(), testEmployeeID, testManagerID); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(decisions.observations) != 1 || decisions.observations[0].Outcome != string(StatusInsufficientEvidence) {
		t.Fatalf("decision observations = %#v, want one bounded insufficient-evidence decision", decisions.observations)
	}
}

func TestServicePreservesRetryableStaleInputResult(t *testing.T) {
	snapshot := acceptedPairSnapshot()
	snapshot.Signals = snapshot.Signals[:1]
	repository := &fakeRepository{snapshot: snapshot, completeErr: ErrStaleAnalysisInput}
	service := testService(repository, &fakeModel{})

	_, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if !errors.Is(err, ErrStaleAnalysisInput) {
		t.Fatalf("Analyze() error = %v, want retryable stale-input result", err)
	}
}

func TestServiceRejectsCachedReportWhenDecisionChangesBetweenLoadAndLookup(t *testing.T) {
	snapshot := acceptedPairSnapshot()
	repository := &fakeRepository{
		snapshot: snapshot,
		cached: Report{
			ID: "historical-run", Status: StatusMixedOrConflicting,
		},
		findErr: ErrStaleAnalysisInput,
	}
	model := &fakeModel{}
	service := testService(repository, model)

	_, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if !errors.Is(err, ErrStaleAnalysisInput) {
		t.Fatalf("Analyze() error = %v, want stale cache identity result", err)
	}
	if got := referenceIDs(repository.findIdentity.Inputs); !slices.Equal(got, referenceIDs(snapshot.Inputs)) {
		t.Fatalf("cache identity inputs = %#v, want loaded snapshot inputs", got)
	}
	if repository.findIdentity.EmployeeEntityID != testEmployeeID || repository.findIdentity.ManagerEntityID != testManagerID ||
		repository.findIdentity.PromptVersion != extract.AnalysisPromptVersion || repository.findIdentity.PolicyVersion != AnalysisPolicyVersion ||
		repository.findIdentity.InputDigest == ([sha256.Size]byte{}) {
		t.Fatalf("cache identity = %#v, want complete pair, version, and digest identity", repository.findIdentity)
	}
	if model.calls != 0 || repository.completeCalls != 0 {
		t.Fatalf("model/complete calls = %d/%d, want 0/0 after stale cache lookup", model.calls, repository.completeCalls)
	}
}

func TestServiceUsesExactOrderedInputDigestAndCachesIdenticalRun(t *testing.T) {
	repository := &fakeRepository{snapshot: acceptedPairSnapshot()}
	model := &fakeModel{output: analysisOutput(StatusMixedOrConflicting, []string{"signal-earlier"}, nil)}
	service := testService(repository, model)

	first, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("first Analyze() error = %v", err)
	}
	wantDigest, err := ComputeInputDigest(AnalysisIdentity{
		EmployeeEntityID: testEmployeeID,
		ManagerEntityID:  testManagerID,
		PromptVersion:    extract.AnalysisPromptVersion,
		PolicyVersion:    AnalysisPolicyVersion,
		Inputs:           repository.snapshot.Inputs,
	})
	if err != nil {
		t.Fatalf("ComputeInputDigest() error = %v", err)
	}
	if repository.completed.Identity.InputDigest != wantDigest {
		t.Fatalf("persisted input digest = %x, want %x", repository.completed.Identity.InputDigest, wantDigest)
	}
	if got := referenceIDs(repository.completed.Identity.Inputs); !slices.Equal(got, referenceIDs(repository.snapshot.Inputs)) {
		t.Fatalf("persisted ordered inputs = %#v, want %#v", got, referenceIDs(repository.snapshot.Inputs))
	}

	repository.cached = first
	second, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("second Analyze() error = %v", err)
	}
	if second.ID != first.ID || model.calls != 1 || repository.completeCalls != 1 {
		t.Fatalf("cached run = %#v, model/complete calls = %d/%d", second, model.calls, repository.completeCalls)
	}
}

func TestServiceCorrectionCreatesNewIdentityWithoutRemovingPriorProvenance(t *testing.T) {
	repository := &fakeRepository{snapshot: acceptedPairSnapshot()}
	model := &fakeModel{output: analysisOutput(StatusNoMaterialChange, []string{"signal-earlier"}, nil)}
	service := testService(repository, model)

	first, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("first Analyze() error = %v", err)
	}
	firstDigest := repository.completed.Identity.InputDigest
	firstInputs := append([]InputReference(nil), repository.completed.Identity.Inputs...)

	correctedDigest := sha256.Sum256([]byte("corrected-decision"))
	repository.snapshot.Inputs[1] = InputReference{Kind: InputResolutionDecision, ID: testCorrectionDecisionID, Digest: correctedDigest}
	repository.cached = Report{}
	second, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("corrected Analyze() error = %v", err)
	}
	if second.ID == first.ID || repository.completed.Identity.InputDigest == firstDigest {
		t.Fatal("corrected resolution decision reused prior analysis identity")
	}
	if !slices.Equal(referenceIDs(repository.history[0].Identity.Inputs), referenceIDs(firstInputs)) {
		t.Fatal("prior run input provenance was mutated by correction")
	}
}

func TestServicePendingAcceptanceAndCorrectionChangeEligibilityWithoutReingest(t *testing.T) {
	full := acceptedPairSnapshot()
	pending := full
	pending.Signals = nil
	pending.Inputs = append([]InputReference(nil), full.Inputs[:2]...)
	repository := &fakeRepository{snapshot: pending}
	model := &fakeModel{output: analysisOutput(StatusNoMaterialChange, []string{"signal-earlier"}, nil)}
	service := testService(repository, model)

	pendingReport, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("pending Analyze() error = %v", err)
	}
	if pendingReport.Status != StatusInsufficientEvidence || model.calls != 0 {
		t.Fatalf("pending report/model calls = %q/%d, want insufficient/0", pendingReport.Status, model.calls)
	}

	repository.snapshot = full
	acceptedReport, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("accepted Analyze() error = %v", err)
	}
	if acceptedReport.Status != StatusMixedOrConflicting || model.calls != 1 || acceptedReport.InputDigest == pendingReport.InputDigest {
		t.Fatalf("accepted report/model calls = %#v/%d", acceptedReport, model.calls)
	}

	correctedDigest := sha256.Sum256([]byte("corrected-away-from-pair"))
	repository.snapshot = PairSnapshot{
		Accepted: true,
		Inputs: []InputReference{
			full.Inputs[0],
			{Kind: InputResolutionDecision, ID: testCorrectionDecisionID, Digest: correctedDigest},
		},
	}
	correctedReport, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("corrected Analyze() error = %v", err)
	}
	if correctedReport.Status != StatusInsufficientEvidence || correctedReport.InputDigest == acceptedReport.InputDigest {
		t.Fatalf("corrected report = %#v, want new insufficient run", correctedReport)
	}
	if repository.history[1].Report.InputDigest != acceptedReport.InputDigest || len(repository.history[1].Report.Chronology) != 2 {
		t.Fatal("accepted run and its original provenance were not retained after correction")
	}
}

func TestServiceSendsOnlyValidatedTranscriptBackedSignalsAndPreservesCounterevidence(t *testing.T) {
	snapshot := acceptedPairSnapshot()
	snapshot.Signals[0].Citations = append(snapshot.Signals[0].Citations, Citation{
		ID: "signal-earlier-counter", Role: CitationContradicting, Quote: "Synthetic counterevidence.",
	})
	unknown := testSignal("signal-unknown", nil, DirectionUnclear)
	notesOnly := testSignal("signal-notes", ptrTime(testMeetingDate(2026, time.July, 15)), DirectionWeakening)
	notesOnly.TranscriptBacked = false
	invalid := testSignal("signal-invalid", ptrTime(testMeetingDate(2026, time.July, 16)), DirectionWeakening)
	invalid.Validated = false
	unidentifiedMeeting := testSignal("signal-unidentified-meeting", ptrTime(testMeetingDate(2026, time.July, 17)), DirectionWeakening)
	unidentifiedMeeting.MeetingID = ""
	snapshot.Signals = append(snapshot.Signals, unknown, notesOnly, invalid, unidentifiedMeeting)
	repository := &fakeRepository{snapshot: snapshot}
	model := &fakeModel{output: analysisOutput(
		StatusMixedOrConflicting,
		[]string{"signal-later"},
		[]string{"signal-earlier"},
	)}
	service := testService(repository, model)

	report, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	var request struct {
		Signals []struct {
			ID string `json:"id"`
		} `json:"signals"`
	}
	if err := json.Unmarshal([]byte(model.request.Input), &request); err != nil {
		t.Fatalf("decode model input: %v", err)
	}
	if got := requestSignalIDs(request.Signals); !slices.Equal(got, []string{"signal-earlier", "signal-later"}) {
		t.Fatalf("model signal IDs = %#v, want validated dated transcript inputs", got)
	}
	if got := signalIDs(report.UnknownTime); !slices.Equal(got, []string{"signal-unknown"}) {
		t.Fatalf("unknown-time report IDs = %#v, want separately preserved validated signal", got)
	}
	if got := signalIDs(report.Counterevidence); !slices.Equal(got, []string{"signal-earlier"}) {
		t.Fatalf("counterevidence IDs = %#v, want cited model counterevidence", got)
	}
	if len(repository.completed.Report.Chronology[0].Citations) == 0 || repository.completed.Report.Chronology[0].Citations[0].Quote == "" {
		t.Fatal("completed report did not preserve exact citation provenance")
	}
}

func TestServiceDowngradesUnsupportedModelDeclineAndRecordsBoundedDecision(t *testing.T) {
	repository := &fakeRepository{snapshot: acceptedPairSnapshot()}
	modelRationale := "The manager has lost confidence and no longer trusts the employee."
	model := &fakeModel{output: analysisOutputWithRationale(StatusPossibleDecline, modelRationale, []string{"signal-later"}, []string{"signal-earlier"})}
	decisions := &fakeDecisionRecorder{}
	service := testService(repository, model)
	service.Decisions = decisions

	report, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Status != StatusMixedOrConflicting {
		t.Fatalf("report status = %q, want deterministic downgrade", report.Status)
	}
	reportProse := strings.ToLower(strings.Join(append(append([]string{report.Rationale}, report.Limitations...), report.Gaps...), " "))
	if strings.Contains(reportProse, strings.ToLower(modelRationale)) || strings.Contains(reportProse, "lost confidence") ||
		strings.Contains(reportProse, "no longer trusts") {
		t.Fatalf("downgraded report retained rejected private-state prose: %q", reportProse)
	}
	if !strings.Contains(strings.ToLower(report.Rationale), "policy") {
		t.Fatalf("downgraded rationale = %q, want deterministic policy rationale", report.Rationale)
	}
	if got := signalIDs(report.Counterevidence); len(got) != 0 {
		t.Fatalf("counterevidence IDs = %#v, want model-only unsupported reference excluded", got)
	}
	if len(decisions.observations) != 1 {
		t.Fatalf("decision observations = %d, want 1", len(decisions.observations))
	}
	observation := decisions.observations[0]
	if observation.Name != analysisDecisionName || observation.Outcome != string(StatusMixedOrConflicting) || observation.InputSize != 2 || observation.OutputSize != 1 {
		t.Fatalf("decision observation = %#v", observation)
	}
}

func TestServicePreservesSourceCounterevidenceInSparseReport(t *testing.T) {
	knownDate := testMeetingDate(2026, time.July, 8)
	dated := testSignal("signal-dated-counter", &knownDate, DirectionWeakening)
	dated.Citations = append(dated.Citations, Citation{ID: "dated-counter", Role: CitationContradicting, Quote: "Synthetic dated counterevidence."})
	unknown := testSignal("signal-unknown-counter", nil, DirectionUnclear)
	unknown.Citations = append(unknown.Citations, Citation{ID: "unknown-counter", Role: CitationContradicting, Quote: "Synthetic unknown-time counterevidence."})
	repository := &fakeRepository{snapshot: PairSnapshot{
		Accepted: true,
		Inputs:   append(append([]InputReference(nil), dated.Inputs...), unknown.Inputs...),
		Signals:  []Signal{unknown, dated},
	}}
	model := &fakeModel{}
	service := testService(repository, model)

	report, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Status != StatusInsufficientEvidence || model.calls != 0 {
		t.Fatalf("report/model = %q/%d, want insufficient/0", report.Status, model.calls)
	}
	if got := signalIDs(report.Counterevidence); !slices.Equal(got, []string{"signal-dated-counter", "signal-unknown-counter"}) {
		t.Fatalf("sparse counterevidence IDs = %#v, want dated then unknown source-linked evidence", got)
	}
}

func TestServicePreservesSourceCounterevidenceWhenModelOmitsIt(t *testing.T) {
	snapshot := acceptedPairSnapshot()
	snapshot.Signals[0].Citations = append(snapshot.Signals[0].Citations, Citation{
		ID: "counter-citation", Role: CitationContradicting, Quote: "Synthetic counterevidence.",
	})
	repository := &fakeRepository{snapshot: snapshot}
	model := &fakeModel{output: analysisOutput(StatusMixedOrConflicting, []string{"signal-later"}, nil)}
	service := testService(repository, model)

	report, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if got := signalIDs(report.Counterevidence); !slices.Equal(got, []string{"signal-earlier"}) {
		t.Fatalf("counterevidence IDs = %#v, want source-linked counterevidence preserved", got)
	}
}

func TestServiceUsesModelDesignatedSourceBackedCounterSignalForAdmittedMixedReport(t *testing.T) {
	snapshot := acceptedPairSnapshot()
	snapshot.Signals[0].Citations = append(snapshot.Signals[0].Citations, Citation{
		ID: "signal-earlier-notes", Role: CitationSupporting, Quote: "Synthetic notes evidence.", Transcript: false,
	})
	repository := &fakeRepository{snapshot: snapshot}
	model := &fakeModel{output: analysisOutput(
		StatusMixedOrConflicting,
		[]string{"signal-later"},
		[]string{"signal-earlier"},
	)}
	service := testService(repository, model)

	report, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Status != StatusMixedOrConflicting {
		t.Fatalf("report status = %q, want admitted mixed report", report.Status)
	}
	if got := signalIDs(report.Counterevidence); !slices.Equal(got, []string{"signal-earlier"}) {
		t.Fatalf("counterevidence IDs = %#v, want model-designated transcript-backed counter-signal", got)
	}
	if len(report.Counterevidence[0].Citations) != 1 || report.Counterevidence[0].Citations[0].Role != CitationSupporting ||
		!report.Counterevidence[0].Citations[0].Transcript {
		t.Fatalf("counter-signal citations = %#v, want its supporting transcript citation", report.Counterevidence[0].Citations)
	}
}

func TestServiceUsesModelDesignatedSourceBackedCounterSignalForAdmittedDecline(t *testing.T) {
	snapshot := acceptedPairSnapshot()
	counterDate := testMeetingDate(2026, time.June, 20)
	counter := testSignal("signal-counter", &counterDate, DirectionStrengthening)
	snapshot.Signals = append(snapshot.Signals, counter)
	snapshot.Inputs = append(snapshot.Inputs, counter.Inputs...)
	repository := &fakeRepository{snapshot: snapshot}
	model := &fakeModel{output: analysisOutput(
		StatusPossibleDecline,
		[]string{"signal-earlier", "signal-later"},
		[]string{"signal-counter"},
	)}
	service := testService(repository, model)

	report, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Status != StatusPossibleDecline {
		t.Fatalf("report status = %q, want admitted decline", report.Status)
	}
	if got := signalIDs(report.Counterevidence); !slices.Equal(got, []string{"signal-counter"}) {
		t.Fatalf("counterevidence IDs = %#v, want admitted strengthening counter-signal", got)
	}
}

func TestServiceDeduplicatesModelDesignatedAndExplicitCounterevidence(t *testing.T) {
	snapshot := acceptedPairSnapshot()
	explicit := Citation{
		ID: "signal-earlier-explicit-counter", Role: CitationContradicting, Quote: "Synthetic explicit counterevidence.",
	}
	snapshot.Signals[0].Citations = append(snapshot.Signals[0].Citations, explicit, explicit)
	repository := &fakeRepository{snapshot: snapshot}
	model := &fakeModel{output: analysisOutput(
		StatusMixedOrConflicting,
		[]string{"signal-later"},
		[]string{"signal-earlier"},
	)}
	service := testService(repository, model)

	report, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if got := signalIDs(report.Counterevidence); !slices.Equal(got, []string{"signal-earlier"}) {
		t.Fatalf("counterevidence IDs = %#v, want one deduplicated signal", got)
	}
	if got := len(report.Counterevidence[0].Citations); got != 2 {
		t.Fatalf("counterevidence citation count = %d, want supporting plus explicit contradicting citation", got)
	}
}

func TestServiceRejectsUnknownModelContradictingSignalID(t *testing.T) {
	repository := &fakeRepository{snapshot: acceptedPairSnapshot()}
	model := &fakeModel{output: analysisOutput(
		StatusMixedOrConflicting,
		[]string{"signal-later"},
		[]string{"signal-unknown"},
	)}
	service := testService(repository, model)

	_, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if !errors.Is(err, ErrInvalidModelOutput) {
		t.Fatalf("Analyze() error = %v, want ErrInvalidModelOutput", err)
	}
}

func TestServiceRejectsAnalysisOutputMissingRequiredArrays(t *testing.T) {
	repository := &fakeRepository{snapshot: acceptedPairSnapshot()}
	model := &fakeModel{output: json.RawMessage(`{"conclusion":"mixed or conflicting signals","rationale":"Synthetic rationale."}`)}
	service := testService(repository, model)

	_, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if !errors.Is(err, ErrInvalidModelOutput) {
		t.Fatalf("Analyze() error = %v, want ErrInvalidModelOutput", err)
	}
}

func TestServiceFinishesOneBoundedAnalysisSpanWithExplicitOK(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	repository := &fakeRepository{snapshot: acceptedPairSnapshot()}
	service := testService(repository, &fakeModel{output: analysisOutput(StatusMixedOrConflicting, []string{"signal-later"}, nil)})
	service.Tracer = provider.Tracer("synthetic")

	if _, err := service.Analyze(context.Background(), testEmployeeID, testManagerID); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != analysisSpanName || spans[0].Status.Code != codes.Ok {
		t.Fatalf("spans = %#v, want one explicitly OK analysis span", spans)
	}
	for _, value := range spans[0].Attributes {
		rendered := value.Value.String()
		if strings.Contains(rendered, testEmployeeID) || strings.Contains(rendered, "Synthetic") {
			t.Fatalf("analysis span attribute %q contains private or unbounded input", value.Key)
		}
	}
}

func TestComputeInputDigestCanonicalizesPairAndInputUUIDs(t *testing.T) {
	digest := sha256.Sum256([]byte("input"))
	canonical := AnalysisIdentity{
		EmployeeEntityID: testEmployeeID, ManagerEntityID: testManagerID,
		PromptVersion: "analyze-v1", PolicyVersion: "policy-v1",
		Inputs: []InputReference{{Kind: InputSignal, ID: "99999999-aaaa-bbbb-cccc-dddddddddddd", Digest: digest}},
	}
	variant := canonical
	variant.EmployeeEntityID = strings.ToUpper(canonical.EmployeeEntityID)
	variant.Inputs = append([]InputReference(nil), canonical.Inputs...)
	variant.Inputs[0].ID = strings.ToUpper(canonical.Inputs[0].ID)

	first, err := ComputeInputDigest(canonical)
	if err != nil {
		t.Fatalf("canonical ComputeInputDigest() error = %v", err)
	}
	second, err := ComputeInputDigest(variant)
	if err != nil {
		t.Fatalf("variant ComputeInputDigest() error = %v", err)
	}
	if first != second {
		t.Fatal("equivalent UUID spellings produced different analysis identities")
	}
}

func TestComputeInputDigestRejectsRepeatedInputIdentity(t *testing.T) {
	digest := sha256.Sum256([]byte("input"))
	input := InputReference{Kind: InputSignal, ID: "99999999-aaaa-bbbb-cccc-dddddddddddd", Digest: digest}
	_, err := ComputeInputDigest(AnalysisIdentity{
		EmployeeEntityID: testEmployeeID, ManagerEntityID: testManagerID,
		PromptVersion: "analyze-v1", PolicyVersion: "policy-v1",
		Inputs: []InputReference{input, input},
	})
	if err == nil {
		t.Fatal("ComputeInputDigest() error = nil, want repeated input identity rejection")
	}
}

func TestComputeInputDigestAllowsPairIdentityWithoutSignalInputs(t *testing.T) {
	first, err := ComputeInputDigest(AnalysisIdentity{
		EmployeeEntityID: testEmployeeID, ManagerEntityID: testManagerID,
		PromptVersion: "analyze-v1", PolicyVersion: "policy-v1",
	})
	if err != nil {
		t.Fatalf("ComputeInputDigest() error = %v", err)
	}
	second, err := ComputeInputDigest(AnalysisIdentity{
		EmployeeEntityID: testEmployeeID, ManagerEntityID: testManagerID,
		PromptVersion: "analyze-v1", PolicyVersion: "policy-v1",
	})
	if err != nil {
		t.Fatalf("repeated ComputeInputDigest() error = %v", err)
	}
	if first == ([sha256.Size]byte{}) || first != second {
		t.Fatalf("empty-input identity = %x/%x, want stable non-zero digest", first, second)
	}
}

func TestAnalysisPolicyVersionChangesForCounterevidenceSemantics(t *testing.T) {
	if AnalysisPolicyVersion != "manager-confidence-policy-v3" {
		t.Fatalf("AnalysisPolicyVersion = %q, want cache-invalidating counterevidence policy version", AnalysisPolicyVersion)
	}
}

const (
	testEmployeeID           = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testManagerID            = "11111111-2222-3333-4444-555555555555"
	testEmployeeDecisionID   = "00000000-0000-0000-0000-000000000001"
	testManagerDecisionID    = "00000000-0000-0000-0000-000000000002"
	testCorrectionDecisionID = "00000000-0000-0000-0000-000000000003"
)

func acceptedPairSnapshot() PairSnapshot {
	earlier := testMeetingDate(2026, time.June, 3)
	later := testMeetingDate(2026, time.July, 8)
	employeeDigest := sha256.Sum256([]byte("employee-decision"))
	managerDigest := sha256.Sum256([]byte("manager-decision"))
	snapshot := PairSnapshot{
		Accepted: true,
		Inputs: []InputReference{
			{Kind: InputResolutionDecision, ID: testEmployeeDecisionID, Digest: employeeDigest},
			{Kind: InputResolutionDecision, ID: testManagerDecisionID, Digest: managerDigest},
		},
		Signals: []Signal{
			testSignal("signal-earlier", &earlier, DirectionStrengthening),
			testSignal("signal-later", &later, DirectionWeakening),
		},
	}
	for _, signal := range snapshot.Signals {
		snapshot.Inputs = append(snapshot.Inputs, signal.Inputs...)
	}
	return snapshot
}

func testSignal(id string, validTime *time.Time, direction Direction) Signal {
	observationDigest := sha256.Sum256([]byte(id + "-observation"))
	signalDigest := sha256.Sum256([]byte(id + "-signal"))
	documentDigest := sha256.Sum256([]byte(id + "-document"))
	tabDigest := sha256.Sum256([]byte(id + "-tab"))
	return Signal{
		ID: id, MeetingID: id + "-meeting", ObservationID: id + "-observation", Category: CategoryDelegationAutonomy,
		Direction: direction, ValidTime: validTime,
		RecordedAt: time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC),
		Rationale:  "Synthetic observable interaction rationale.", Confidence: 0.5,
		Validated: true, TranscriptBacked: true,
		Inputs: []InputReference{
			{Kind: InputDocumentVersion, ID: id + "-document", Digest: documentDigest},
			{Kind: InputDocumentTab, ID: id + "-tab", Digest: tabDigest},
			{Kind: InputObservation, ID: id + "-observation", Digest: observationDigest},
			{Kind: InputSignal, ID: id, Digest: signalDigest},
		},
		Citations: []Citation{{
			ID: id + "-citation", ProviderDocumentID: id + "-document-provider",
			ProviderTabID: id + "-tab-provider", StartOffset: 4, EndOffset: 13,
			Quote: "Synthetic", Role: CitationSupporting, Transcript: true,
		}},
	}
}

func analysisOutput(status ReportStatus, supporting, contradicting []string) json.RawMessage {
	return analysisOutputWithRationale(status, "A bounded synthesis of observable changes.", supporting, contradicting)
}

func analysisOutputWithRationale(status ReportStatus, rationale string, supporting, contradicting []string) json.RawMessage {
	if supporting == nil {
		supporting = []string{}
	}
	if contradicting == nil {
		contradicting = []string{}
	}
	output, _ := json.Marshal(map[string]any{
		"conclusion":               status,
		"rationale":                rationale,
		"supporting_signal_ids":    supporting,
		"contradicting_signal_ids": contradicting,
		"gaps":                     []string{"No direct observation outside scheduled meetings."},
	})
	return output
}

func testService(repository *fakeRepository, model *fakeModel) *Service {
	return &Service{
		Repository:    repository,
		Model:         model,
		PromptVersion: extract.AnalysisPromptVersion,
		Region:        "us-east-1", ModelID: "synthetic-model", MaxTokens: 256,
		Now: func() time.Time { return time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC) },
	}
}

type fakeRepository struct {
	snapshot      PairSnapshot
	cached        Report
	findIdentity  AnalysisIdentity
	findErr       error
	completed     Completion
	history       []Completion
	completeCalls int
	completeErr   error
}

func (repository *fakeRepository) LoadPairInputs(context.Context, string, string) (PairSnapshot, error) {
	return clonePairSnapshot(repository.snapshot), nil
}

func (repository *fakeRepository) FindCompleted(_ context.Context, identity AnalysisIdentity) (Report, bool, error) {
	repository.findIdentity = identity
	repository.findIdentity.Inputs = append([]InputReference(nil), identity.Inputs...)
	if repository.findErr != nil {
		return Report{}, false, repository.findErr
	}
	if repository.cached.ID != "" && repository.cached.InputDigest == identity.InputDigest {
		return repository.cached, true, nil
	}
	return Report{}, false, nil
}

func (repository *fakeRepository) CompleteAnalysis(_ context.Context, completion Completion) (Report, error) {
	repository.completeCalls++
	if repository.completeErr != nil {
		return Report{}, repository.completeErr
	}
	completion.Report.ID = "run-" + completion.Identity.InputDigestString()
	completion.Report.InputDigest = completion.Identity.InputDigest
	repository.completed = completion
	repository.history = append(repository.history, cloneCompletion(completion))
	return completion.Report, nil
}

type fakeModel struct {
	output  json.RawMessage
	request extract.Request
	calls   int
}

func (model *fakeModel) Generate(_ context.Context, request extract.Request) (extract.Response, error) {
	model.calls++
	model.request = request
	return extract.Response{
		Output: model.output, ModelID: "synthetic-model",
		PromptVersion: request.PromptVersion, Outcome: "success",
	}, nil
}

type fakeDecisionRecorder struct {
	observations []observability.DecisionObservation
}

func (recorder *fakeDecisionRecorder) Record(_ context.Context, observation observability.DecisionObservation) error {
	recorder.observations = append(recorder.observations, observation)
	return nil
}

func ptrTime(value time.Time) *time.Time { return &value }

func referenceIDs(inputs []InputReference) []string {
	ids := make([]string, len(inputs))
	for index, input := range inputs {
		ids[index] = input.ID
	}
	return ids
}

func requestSignalIDs(signals []struct {
	ID string `json:"id"`
}) []string {
	ids := make([]string, len(signals))
	for index, signal := range signals {
		ids[index] = signal.ID
	}
	return ids
}

func clonePairSnapshot(snapshot PairSnapshot) PairSnapshot {
	snapshot.Inputs = append([]InputReference(nil), snapshot.Inputs...)
	snapshot.Signals = append([]Signal(nil), snapshot.Signals...)
	return snapshot
}

func cloneCompletion(completion Completion) Completion {
	completion.Identity.Inputs = append([]InputReference(nil), completion.Identity.Inputs...)
	return completion
}
