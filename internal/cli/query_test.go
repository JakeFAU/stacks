package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"

	"stacks/internal/query"
)

func TestQueryCommandRendersSelectedFormatAfterOneTypedQuery(t *testing.T) {
	for _, output := range []QueryOutput{QueryOutputText, QueryOutputJSON} {
		t.Run(string(output), func(t *testing.T) {
			result := populatedTrendResult(t, false)
			request := requestFromResult(result)
			service := &recordingQueryService{result: result}
			var rendered bytes.Buffer

			err := (QueryCommand{Service: service, Output: &rendered}).Run(
				context.Background(),
				queryInvocation(request, output),
			)

			if err != nil {
				t.Fatalf("QueryCommand.Run() error = %v", err)
			}
			if service.calls != 1 || !reflect.DeepEqual(service.request, request) {
				t.Fatalf("Query() calls/request = %d/%#v, want 1/%#v", service.calls, service.request, request)
			}
			if rendered.Len() == 0 {
				t.Fatal("QueryCommand.Run() output is empty")
			}
		})
	}
}

func TestQueryCommandRejectsInvalidInvocationBeforeService(t *testing.T) {
	service := &recordingQueryService{result: populatedTrendResult(t, false)}
	var rendered bytes.Buffer

	err := (QueryCommand{Service: service, Output: &rendered}).Run(
		context.Background(),
		Invocation{Command: CommandQuery, Action: ActionTrend},
	)

	if err == nil {
		t.Fatal("QueryCommand.Run() error = nil, want invalid invocation error")
	}
	if service.calls != 0 {
		t.Fatalf("Query() calls = %d, want 0", service.calls)
	}
	if rendered.Len() != 0 {
		t.Fatalf("output = %q, want empty", rendered.String())
	}
}

func TestQueryCommandPreservesWriterFailureAfterOneBufferedWrite(t *testing.T) {
	want := errors.New("synthetic writer failure")
	writer := &failingQueryWriter{err: want}
	result := populatedTrendResult(t, false)

	err := (QueryCommand{
		Service: &recordingQueryService{result: result},
		Output:  writer,
	}).Run(context.Background(), queryInvocation(requestFromResult(result), QueryOutputJSON))

	if !errors.Is(err, want) {
		t.Fatalf("QueryCommand.Run() error = %v, want wrapped writer failure", err)
	}
	if writer.calls != 1 {
		t.Fatalf("Write() calls = %d, want 1", writer.calls)
	}
}

func TestQueryRendererRejectsMalformedUnionWithoutPartialOutput(t *testing.T) {
	result := populatedTrendResult(t, false)
	result.Payload = query.IntentPayload{}

	for _, output := range []QueryOutput{QueryOutputText, QueryOutputJSON} {
		t.Run(string(output), func(t *testing.T) {
			var rendered bytes.Buffer
			err := (QueryCommand{
				Service: &recordingQueryService{result: result},
				Output:  &rendered,
			}).Run(context.Background(), queryInvocation(requestFromResult(result), output))

			if err == nil {
				t.Fatal("QueryCommand.Run() error = nil, want malformed union error")
			}
			if rendered.Len() != 0 {
				t.Fatalf("output = %q, want no partial output", rendered.String())
			}
		})
	}
}

type recordingQueryService struct {
	result  query.Result
	err     error
	request query.Request
	calls   int
}

func (service *recordingQueryService) Query(_ context.Context, request query.Request) (query.Result, error) {
	service.calls++
	service.request = request
	return service.result, service.err
}

type failingQueryWriter struct {
	err   error
	calls int
}

func (writer *failingQueryWriter) Write([]byte) (int, error) {
	writer.calls++
	return 0, writer.err
}

func queryInvocation(request query.Request, output QueryOutput) Invocation {
	return Invocation{
		Command: CommandQuery,
		Action:  ActionTrend,
		Query:   &QueryInput{Request: request, Output: output},
	}
}

func requestFromResult(result query.Result) query.Request {
	return query.Request{
		Intent:         result.Intent,
		EntityIDs:      append([]identity.EntityID{}, result.EntityIDs...),
		EntityMatch:    result.EntityMatch,
		Predicates:     append([]observation.Predicate{}, result.Predicates...),
		Selections:     append([]temporal.TemporalSelection{}, result.Selections...),
		KnowledgeScope: result.KnowledgeScope,
		Limit:          result.Limit,
	}
}

func populatedTrendResult(t *testing.T, reverse bool) query.Result {
	t.Helper()
	before := mustQueryWindow(t, "before", testInstant(2026, time.January, 1), testInstant(2026, time.February, 1))
	after := mustQueryWindow(t, "after", testInstant(2026, time.March, 1), testInstant(2026, time.April, 1))
	asOf, err := temporal.KnownAsOf(time.Date(2026, time.May, 1, 1, 2, 3, 987654321, time.FixedZone("east", 2*60*60)))
	if err != nil {
		t.Fatal(err)
	}

	changedKey := mustQueryKey(t, mustQueryEntity(t, "entity-a"), "a.changed")
	removedKey := mustQueryKey(t, mustQueryText(t, "subject bytes"), "b.removed")
	addedKey := mustQueryKey(t, observation.AbsentTerm(), "c.added")
	conflictKey := mustQueryKey(t, mustQueryEntity(t, "entity-b"), "d.conflict")
	hypothesisKey := mustQueryKey(t, mustQueryText(t, "hypothesis subject"), "e.hypothesis")

	changedBefore := query.Fact{
		Key:   changedKey,
		Value: mustQueryText(t, "remote"),
		Contributions: maybeReverse(reverse, []query.Contribution{
			testContribution(t, "observation-changed-b", observation.StatusInferred, mustQueryInstant(t, testInstant(2026, time.January, 16)), observation.Derivation{Method: "extract", Version: "v2", RunID: "run-2", Model: "synthetic-model", PromptVersion: "prompt-v3"}, "mention-subject", "mention-object"),
			testContribution(t, "observation-changed-a", observation.StatusObserved, observation.UnknownTime(), observation.Derivation{Method: "manual", Version: "v1"}, "", ""),
		}),
		SupportingCitations: maybeReverse(reverse, []query.Citation{
			testCitation("evidence-support-b", observation.EvidenceSupporting, true),
			testCitation("evidence-support-a", observation.EvidenceSupporting, false),
		}),
		ContradictingCitations: []query.Citation{
			testCitation("evidence-counter-a", observation.EvidenceContradicting, true),
		},
	}
	changedAfter := testFact(t, changedKey, mustQueryText(t, "office"),
		testContribution(t, "observation-after", observation.StatusValidatedEmpirically, mustQueryDuring(t, testInstant(2026, time.March, 1), testInstant(2026, time.April, 1)), observation.Derivation{Method: "review", Version: "v4"}, "", ""),
		testCitation("evidence-after", observation.EvidenceSupporting, true),
	)
	removed := testFact(t, removedKey, mustQueryEntity(t, "entity-owner"),
		testContribution(t, "observation-removed", observation.StatusHypothesized, mustQuerySince(t, testInstant(2025, time.December, 1)), observation.Derivation{Method: "extract", Version: "v1"}, "", ""),
		testCitation("evidence-removed", observation.EvidenceSupporting, false),
	)
	added := testFact(t, addedKey, observation.AbsentTerm(),
		testContribution(t, "observation-added", observation.StatusValidatedStructurally, mustQueryUntil(t, testInstant(2026, time.April, 1)), observation.Derivation{Method: "rule", Version: "v5"}, "", ""),
		testCitation("evidence-added", observation.EvidenceSupporting, false),
	)
	conflictCandidateA := testFact(t, conflictKey, mustQueryText(t, "alpha"),
		testContribution(t, "observation-conflict-a", observation.StatusRejected, mustQueryWithin(t, testInstant(2026, time.January, 10), testInstant(2026, time.January, 20)), observation.Derivation{Method: "review", Version: "v2"}, "", ""),
		testCitation("evidence-conflict-a", observation.EvidenceSupporting, false),
	)
	conflictCandidateB := testFact(t, conflictKey, mustQueryText(t, "beta"),
		testContribution(t, "observation-conflict-b", observation.StatusValidatedEmpirically, mustQueryDuring(t, testInstant(2026, time.January, 11), testInstant(2026, time.January, 21)), observation.Derivation{Method: "review", Version: "v2"}, "", ""),
		testCitation("evidence-conflict-b", observation.EvidenceSupporting, false),
	)
	hypothesisCandidate := testFact(t, hypothesisKey, mustQueryText(t, "candidate"),
		testContribution(t, "observation-hypothesis", observation.StatusHypothesized, mustQueryInstant(t, testInstant(2026, time.March, 12)), observation.Derivation{Method: "extract", Version: "v7"}, "", ""),
		testCitation("evidence-hypothesis", observation.EvidenceContradicting, false),
	)

	changes := []query.Change{
		{Kind: temporal.ChangeAdded, Key: addedKey, After: queryFactPointer(added)},
		{Kind: temporal.ChangeRemoved, Key: removedKey, Before: queryFactPointer(removed)},
		{Kind: temporal.ChangeChanged, Key: changedKey, Before: queryFactPointer(changedBefore), After: queryFactPointer(changedAfter)},
	}
	trend := query.TrendResult{
		Before: query.WindowResult{
			Selection: before,
			Facts:     maybeReverse(reverse, []query.Fact{removed, changedBefore}),
			Unresolved: []query.UnresolvedItem{{
				Key: conflictKey, Reason: temporal.UnresolvedConflict,
				Candidates: maybeReverse(reverse, []query.Fact{conflictCandidateB, conflictCandidateA}),
			}},
		},
		After: query.WindowResult{
			Selection: after,
			Facts:     maybeReverse(reverse, []query.Fact{added, changedAfter}),
			Unresolved: []query.UnresolvedItem{{
				Key: hypothesisKey, Reason: temporal.UnresolvedHypothesis,
				Candidates: []query.Fact{hypothesisCandidate},
			}},
		},
		Changes:        maybeReverse(reverse, changes),
		UnresolvedKeys: maybeReverse(reverse, []temporal.StateKey{hypothesisKey, conflictKey}),
	}
	payload, err := query.NewTrendPayload(trend)
	if err != nil {
		t.Fatalf("query.NewTrendPayload() error = %v", err)
	}
	result := query.Result{
		Intent:         temporal.IntentTrendComparison,
		EntityIDs:      maybeReverse(reverse, []identity.EntityID{"entity-a", "entity-b"}),
		EntityMatch:    query.EntityMatchAll,
		Predicates:     maybeReverse(reverse, []observation.Predicate{"a.changed", "b.removed", "c.added", "d.conflict", "e.hypothesis"}),
		Selections:     []temporal.TemporalSelection{before, after},
		KnowledgeScope: asOf,
		Payload:        payload,
		Gaps: maybeReverse(reverse, []query.Gap{
			{Kind: query.GapValidTimeExcluded, EntityID: "entity-b", Predicate: "b.removed", SelectionLabel: "after"},
			{Kind: query.GapNoEvidence},
		}),
	}
	normalized, err := query.NormalizeResult(result)
	if err != nil {
		t.Fatalf("query.NormalizeResult() error = %v", err)
	}
	if err := query.ValidateResult(normalized); err != nil {
		t.Fatalf("query.ValidateResult() error = %v", err)
	}
	return normalized
}

func testFact(
	t *testing.T,
	key temporal.StateKey,
	value observation.Term,
	contribution query.Contribution,
	citation query.Citation,
) query.Fact {
	t.Helper()
	supporting, contradicting := []query.Citation{}, []query.Citation{}
	if citation.Role == observation.EvidenceSupporting {
		supporting = []query.Citation{citation}
	} else {
		contradicting = []query.Citation{citation}
	}
	return query.Fact{
		Key:                    key,
		Value:                  value,
		Contributions:          []query.Contribution{contribution},
		SupportingCitations:    supporting,
		ContradictingCitations: contradicting,
	}
}

func testContribution(
	t *testing.T,
	id observation.ObservationID,
	status observation.EpistemicStatus,
	validTime observation.TemporalExtent,
	derivation observation.Derivation,
	subjectGrounding string,
	objectGrounding string,
) query.Contribution {
	t.Helper()
	return query.Contribution{
		ObservationID:             id,
		Status:                    status,
		ValidTime:                 validTime,
		RecordedAt:                time.Date(2026, time.June, 1, 5, 6, 7, 123456789, time.FixedZone("west", -4*60*60)),
		Derivation:                derivation,
		SubjectGroundingMentionID: subjectGrounding,
		ObjectGroundingMentionID:  objectGrounding,
	}
}

func testCitation(id evidence.EvidenceID, role observation.EvidenceRole, populatedOptional bool) query.Citation {
	citation := query.Citation{
		EvidenceID:        id,
		Role:              role,
		SourceDocumentID:  "document-" + string(id),
		DocumentVersionID: "version-" + string(id),
		SectionID:         "section-" + string(id),
		SectionTitle:      "Synthetic section",
		SectionPath:       []string{},
		SectionOrder:      2,
		SectionRole:       "body",
		StartOffset:       3,
		EndOffset:         11,
	}
	if populatedOptional {
		citation.SectionPath = []string{"Parent", "Child"}
		citation.Locator = "synthetic://document/" + string(id)
		citation.Text = "exact synthetic bytes"
	}
	return citation
}

func mustQueryWindow(t *testing.T, label string, start, end time.Time) temporal.TemporalSelection {
	t.Helper()
	value, err := temporal.Between(label, start, end)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustQueryKey(t *testing.T, subject observation.Term, predicate observation.Predicate) temporal.StateKey {
	t.Helper()
	value, err := temporal.NewStateKey(subject, predicate)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustQueryText(t *testing.T, value string) observation.Term {
	t.Helper()
	term, err := observation.NewTextTerm(value)
	if err != nil {
		t.Fatal(err)
	}
	return term
}

func mustQueryMention(t *testing.T, value string) observation.Term {
	t.Helper()
	term, err := observation.NewMentionTerm(value)
	if err != nil {
		t.Fatal(err)
	}
	return term
}

func mustQueryEntity(t *testing.T, value string) observation.Term {
	t.Helper()
	term, err := observation.NewEntityTerm(value, "")
	if err != nil {
		t.Fatal(err)
	}
	return term
}

func mustQueryInstant(t *testing.T, at time.Time) observation.TemporalExtent {
	t.Helper()
	value, err := observation.AtTime(at)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustQueryDuring(t *testing.T, start, end time.Time) observation.TemporalExtent {
	t.Helper()
	value, err := observation.During(start, end)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustQuerySince(t *testing.T, start time.Time) observation.TemporalExtent {
	t.Helper()
	value, err := observation.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustQueryUntil(t *testing.T, end time.Time) observation.TemporalExtent {
	t.Helper()
	value, err := observation.Until(end)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustQueryWithin(t *testing.T, start, end time.Time) observation.TemporalExtent {
	t.Helper()
	value, err := observation.Within(start, end)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testInstant(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 8, 9, 10, 654321987, time.FixedZone("test", -5*60*60))
}

func queryFactPointer(value query.Fact) *query.Fact {
	return &value
}

func maybeReverse[T any](reverse bool, values []T) []T {
	result := append([]T{}, values...)
	if reverse {
		for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
			result[left], result[right] = result[right], result[left]
		}
	}
	return result
}

var _ QueryService = (*recordingQueryService)(nil)
var _ io.Writer = (*failingQueryWriter)(nil)
