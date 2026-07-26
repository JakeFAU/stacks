package analysis

import (
	"context"
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
	"stacks/internal/modelpolicy"
	"stacks/internal/observability"
)

func TestServiceUsesOnlyReadOnlyCanonicalRepository(t *testing.T) {
	repository := &readOnlyCanonicalRepository{
		snapshots: []PairSnapshot{{Accepted: true}},
	}
	service := testReadOnlyService(repository, &fakeModel{})

	report, err := service.Analyze(
		context.Background(),
		"employee:opaque/service",
		"manager:opaque/service",
	)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Status != StatusInsufficientEvidence || repository.loadCalls != 1 {
		t.Fatalf(
			"report/load calls = %q/%d, want insufficient/1",
			report.Status,
			repository.loadCalls,
		)
	}
}

func TestServiceDoesNotPersistManagerReport(t *testing.T) {
	repository := &readOnlyCanonicalRepository{
		snapshots: []PairSnapshot{{Accepted: true}},
	}
	service := testReadOnlyService(repository, &fakeModel{})

	first, err := service.Analyze(
		context.Background(),
		"employee:opaque/service",
		"manager:opaque/service",
	)
	if err != nil {
		t.Fatalf("first Analyze() error = %v", err)
	}
	second, err := service.Analyze(
		context.Background(),
		"employee:opaque/service",
		"manager:opaque/service",
	)
	if err != nil {
		t.Fatalf("second Analyze() error = %v", err)
	}
	if first.Status != StatusInsufficientEvidence ||
		second.Status != StatusInsufficientEvidence ||
		repository.loadCalls != 2 {
		t.Fatalf(
			"reports/load calls = %q/%q/%d, want on-demand insufficient/insufficient/2",
			first.Status,
			second.Status,
			repository.loadCalls,
		)
	}
}

func TestServiceRepeatedQueryReevaluatesCurrentAuthority(t *testing.T) {
	repository := &readOnlyCanonicalRepository{
		snapshots: []PairSnapshot{
			{Accepted: true},
			{Accepted: false},
		},
	}
	service := testReadOnlyService(repository, &fakeModel{})

	if _, err := service.Analyze(
		context.Background(),
		"employee:opaque/service",
		"manager:opaque/service",
	); err != nil {
		t.Fatalf("first Analyze() error = %v", err)
	}
	_, err := service.Analyze(
		context.Background(),
		"employee:opaque/service",
		"manager:opaque/service",
	)
	if !errors.Is(err, ErrPairNotAccepted) {
		t.Fatalf("second Analyze() error = %v, want ErrPairNotAccepted", err)
	}
	if repository.loadCalls != 2 {
		t.Fatalf("load calls = %d, want current authority read per call", repository.loadCalls)
	}
}

func TestServicePreservesBoundedAdmissionAndCounterevidence(t *testing.T) {
	meeting := testMeetingDate(2026, time.July, 8)
	signal := testSignal(
		"signal:opaque/counterevidence",
		&meeting,
		DirectionStrengthening,
	)
	signal.Citations = append(signal.Citations, Citation{
		ID: "evidence:opaque/counterevidence", Role: CitationContradicting,
		Quote: "Synthetic counterevidence.",
	})
	repository := &readOnlyCanonicalRepository{
		snapshots: []PairSnapshot{{
			Accepted: true,
			Signals:  []Signal{signal},
		}},
	}
	service := testReadOnlyService(repository, &fakeModel{})

	report, err := service.Analyze(
		context.Background(),
		"employee:opaque/service",
		"manager:opaque/service",
	)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Status != StatusInsufficientEvidence ||
		!slices.Equal(
			signalIDs(report.Counterevidence),
			[]string{"signal:opaque/counterevidence"},
		) {
		t.Fatalf(
			"report = %#v, want bounded insufficient result with source counterevidence",
			report,
		)
	}
}

func TestServiceRequiresAcceptedConfiguredPair(t *testing.T) {
	repository := &fakeRepository{snapshot: PairSnapshot{Accepted: false}}
	model := &fakeModel{}
	service := testService(repository, model)

	_, err := service.Analyze(context.Background(), testEmployeeID, testManagerID)
	if !errors.Is(err, ErrPairNotAccepted) {
		t.Fatalf("Analyze() error = %v, want ErrPairNotAccepted", err)
	}
	if model.calls != 0 {
		t.Fatalf("model calls = %d, want 0", model.calls)
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

func TestServiceRejectsProviderRegionPolicyBeforeRepositoryAccess(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Service)
	}{
		{"missing provider", func(service *Service) { service.Provider = "" }},
		{"bedrock missing region", func(service *Service) { service.Region = "" }},
		{"bedrock padded region", func(service *Service) { service.Region = " us-east-1 " }},
		{"openai region", func(service *Service) {
			service.Provider = modelpolicy.ProviderOpenAI
			service.Region = "us-east-1"
		}},
		{"restricted direct provider", func(service *Service) {
			service.Provider = modelpolicy.ProviderAnthropic
			service.DataMode = modelpolicy.DataModeRestricted
			service.Region = ""
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{snapshot: acceptedPairSnapshot()}
			model := &fakeModel{}
			service := testService(repository, model)
			test.mutate(service)

			if _, err := service.Analyze(context.Background(), testEmployeeID, testManagerID); err == nil {
				t.Fatal("Analyze() error = nil, want provider policy rejection")
			}
			if repository.loadCalls != 0 || model.calls != 0 {
				t.Fatalf(
					"load/model calls = %d/%d, want validation before boundaries",
					repository.loadCalls,
					model.calls,
				)
			}
		})
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
	if len(report.Chronology[0].Citations) == 0 ||
		report.Chronology[0].Citations[0].Quote == "" {
		t.Fatal("on-demand report did not preserve exact citation provenance")
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

func TestServiceNeverUsesModelNarrativeForAnyAdmittedStatus(t *testing.T) {
	const unsafeNarrative = "The manager secretly distrusts the employee and intends to remove them."
	tests := []struct {
		name       string
		status     ReportStatus
		mutate     func(*PairSnapshot)
		supporting []string
		contrary   []string
	}{
		{name: "insufficient", status: StatusInsufficientEvidence},
		{name: "no material change", status: StatusNoMaterialChange, mutate: func(snapshot *PairSnapshot) {
			snapshot.Signals[1].Direction = DirectionStrengthening
		}},
		{name: "mixed", status: StatusMixedOrConflicting, supporting: []string{"signal-earlier"}, contrary: []string{"signal-later"}},
		{name: "possible decline", status: StatusPossibleDecline, supporting: []string{"signal-earlier", "signal-later"}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			snapshot := acceptedPairSnapshot()
			if testCase.mutate != nil {
				testCase.mutate(&snapshot)
			}
			repository := &fakeRepository{snapshot: snapshot}
			model := &fakeModel{output: analysisOutputWithNarrative(
				testCase.status, unsafeNarrative, testCase.supporting, testCase.contrary,
				[]string{unsafeNarrative},
			)}

			report, err := testService(repository, model).Analyze(context.Background(), testEmployeeID, testManagerID)
			if err != nil {
				t.Fatalf("Analyze() error = %v", err)
			}
			if report.Status != testCase.status {
				t.Fatalf("report status = %q, want admitted %q", report.Status, testCase.status)
			}
			prose := strings.Join(
				append(
					append([]string{report.Rationale}, report.Limitations...),
					report.Gaps...,
				),
				" ",
			)
			if strings.Contains(prose, unsafeNarrative) ||
				strings.TrimSpace(report.Rationale) == "" {
				t.Fatalf("admitted report prose = %q, want nonempty deterministic explanation without model narrative", prose)
			}
		})
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

func TestAnalysisPolicyVersionChangesForSafeExplanationSemantics(t *testing.T) {
	if AnalysisPolicyVersion != "manager-confidence-policy-v6" {
		t.Fatalf("AnalysisPolicyVersion = %q, want current deterministic policy version", AnalysisPolicyVersion)
	}
}

const (
	testEmployeeID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testManagerID  = "11111111-2222-3333-4444-555555555555"
)

func acceptedPairSnapshot() PairSnapshot {
	earlier := testMeetingDate(2026, time.June, 3)
	later := testMeetingDate(2026, time.July, 8)
	return PairSnapshot{
		Accepted: true,
		Signals: []Signal{
			testSignal("signal-earlier", &earlier, DirectionStrengthening),
			testSignal("signal-later", &later, DirectionWeakening),
		},
	}
}

func testSignal(id string, validTime *time.Time, direction Direction) Signal {
	return Signal{
		ID: id, MeetingID: id + "-meeting", ObservationID: id + "-observation", Category: CategoryDelegationAutonomy,
		Direction: direction, ValidTime: validTime,
		RecordedAt: time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC),
		Rationale:  "Synthetic observable interaction rationale.", Confidence: 0.5,
		Validated: true, TranscriptBacked: true,
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
	return analysisOutputWithNarrative(status, rationale, supporting, contradicting, []string{"No direct observation outside scheduled meetings."})
}

func analysisOutputWithNarrative(status ReportStatus, rationale string, supporting, contradicting, gaps []string) json.RawMessage {
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
		"gaps":                     gaps,
	})
	return output
}

func testService(repository *fakeRepository, model *fakeModel) *Service {
	return &Service{
		Repository:    repository,
		Model:         model,
		PromptVersion: extract.AnalysisPromptVersion,
		Provider:      modelpolicy.ProviderBedrock,
		DataMode:      modelpolicy.DataModePersonal,
		Region:        "us-east-1", ModelID: "synthetic-model", MaxTokens: 256,
		Now: func() time.Time { return time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC) },
	}
}

type fakeRepository struct {
	snapshot  PairSnapshot
	loadCalls int
}

type readOnlyCanonicalRepository struct {
	snapshots []PairSnapshot
	loadCalls int
	err       error
}

func (repository *readOnlyCanonicalRepository) LoadPairInputs(
	context.Context,
	string,
	string,
) (PairSnapshot, error) {
	repository.loadCalls++
	if repository.err != nil {
		return PairSnapshot{}, repository.err
	}
	if len(repository.snapshots) == 0 {
		return PairSnapshot{Accepted: true}, nil
	}
	snapshot := repository.snapshots[0]
	repository.snapshots = repository.snapshots[1:]
	return clonePairSnapshot(snapshot), nil
}

func testReadOnlyService(
	repository *readOnlyCanonicalRepository,
	model *fakeModel,
) *Service {
	return &Service{
		Repository:    repository,
		Model:         model,
		PromptVersion: extract.AnalysisPromptVersion,
		Provider:      modelpolicy.ProviderBedrock,
		DataMode:      modelpolicy.DataModePersonal,
		Region:        "us-east-1",
		ModelID:       "synthetic-model",
		MaxTokens:     256,
		Now: func() time.Time {
			return time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
		},
	}
}

func (repository *fakeRepository) LoadPairInputs(context.Context, string, string) (PairSnapshot, error) {
	repository.loadCalls++
	return clonePairSnapshot(repository.snapshot), nil
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
	snapshot.Signals = append([]Signal(nil), snapshot.Signals...)
	return snapshot
}
