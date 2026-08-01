package queryplan

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
	"stacks/internal/modelpolicy"
	"stacks/internal/query"
)

const executableTrajectoryProposal = `{
  "status": "executable",
  "reason": "none",
  "intent": "trajectory",
  "entity_match": "all",
  "predicates": [],
  "selections": [
    {"kind": "window", "label": "between", "at": "", "start": "2026-06-01T00:00:00Z", "end": "2026-07-01T00:00:00Z"}
  ],
  "knowledge_scope": {"kind": "current", "as_of": ""},
  "chronology_limit": 2
}`

type modelFunc func(context.Context, ModelRequest) (ModelResponse, error)

func (fn modelFunc) Plan(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	return fn(ctx, request)
}

type executorFunc func(context.Context, query.Request) (query.Result, error)

func (fn executorFunc) Query(ctx context.Context, request query.Request) (query.Result, error) {
	return fn(ctx, request)
}

type recorderFunc func(context.Context, int64)

func (fn recorderFunc) RecordQuestionBytes(ctx context.Context, value int64) {
	fn(ctx, value)
}

func BenchmarkServiceAskLargePointResult(b *testing.B) {
	request, err := composeRequest(
		[]byte(executablePointProposal),
		[]identity.EntityID{"entity-atlas-001"},
		plannerLimits(),
	)
	if err != nil {
		b.Fatal(err)
	}
	result := benchmarkPointResult(b, request, 1000)
	service := validService(
		modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
			return syntheticModelResponse([]byte(executablePointProposal)), nil
		}),
		executorFunc(func(context.Context, query.Request) (query.Result, error) {
			return result, nil
		}),
	)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := service.Ask(context.Background(), serviceInput()); err != nil {
			b.Fatal(err)
		}
	}
}

func TestServiceAskRejectsInvalidInputBeforeRecorderModelAndExecutor(t *testing.T) {
	calls := []string{}
	service := Service{
		Model: modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
			calls = append(calls, "model")
			return ModelResponse{}, nil
		}),
		Executor: executorFunc(func(context.Context, query.Request) (query.Result, error) {
			calls = append(calls, "executor")
			return query.Result{}, nil
		}),
		Limits:           plannerLimits(),
		PlannerTimeout:   time.Second,
		MaxQuestionBytes: 1024,
		QuestionRecorder: recorderFunc(func(context.Context, int64) { calls = append(calls, "record-question-bytes") }),
	}

	_, err := service.Ask(context.Background(), Input{
		Question:      " \t\n",
		EntityIDs:     []identity.EntityID{"entity-atlas-001"},
		ReferenceTime: serviceReferenceTime(),
	})
	if err == nil {
		t.Fatal("Ask() error = nil")
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %v, want no recorder, model, or executor calls", calls)
	}
}

func TestServiceAskRejectsUnserializableReferenceTimeBeforeRecorderModelAndExecutor(t *testing.T) {
	for _, referenceTime := range []time.Time{
		time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(-1, time.January, 1, 0, 0, 0, 0, time.UTC),
	} {
		calls := []string{}
		service := Service{
			Model: modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
				calls = append(calls, "model")
				return ModelResponse{}, nil
			}),
			Executor: executorFunc(func(context.Context, query.Request) (query.Result, error) {
				calls = append(calls, "executor")
				return query.Result{}, nil
			}),
			Limits:           plannerLimits(),
			PlannerTimeout:   time.Second,
			MaxQuestionBytes: 1024,
			QuestionRecorder: recorderFunc(func(context.Context, int64) { calls = append(calls, "record-question-bytes") }),
		}

		_, err := service.Ask(context.Background(), Input{
			Question:      "What changed?",
			EntityIDs:     []identity.EntityID{"entity-atlas-001"},
			ReferenceTime: referenceTime,
		})
		if err == nil {
			t.Fatal("Ask() error = nil")
		}
		if len(calls) != 0 {
			t.Fatalf("calls = %v, want no recorder, model, or executor calls", calls)
		}
	}
}

func TestServiceAskRecordsQuestionBytesBeforePlanningAndExecutesNormalizedRequest(t *testing.T) {
	calls := []string{}
	input := Input{
		Question:      "What was assigned?",
		EntityIDs:     []identity.EntityID{"entity-atlas-002", "entity-atlas-001"},
		ReferenceTime: time.Date(2026, time.July, 29, 12, 0, 0, 123456789, time.FixedZone("synthetic", -4*60*60)),
	}
	wantRequest, err := composeRequest([]byte(executablePointProposal), []identity.EntityID{"entity-atlas-001", "entity-atlas-002"}, plannerLimits())
	if err != nil {
		t.Fatal(err)
	}
	wantResult := syntheticPointResult(t, wantRequest)
	service := Service{
		Model: modelFunc(func(_ context.Context, request ModelRequest) (ModelResponse, error) {
			calls = append(calls, "model")
			for _, entityID := range input.EntityIDs {
				if strings.Contains(request.Input, string(entityID)) {
					t.Fatalf("model request disclosed canonical entity ID %q", entityID)
				}
			}
			return syntheticModelResponse([]byte(executablePointProposal)), nil
		}),
		Executor: executorFunc(func(_ context.Context, request query.Request) (query.Result, error) {
			calls = append(calls, "executor")
			if !reflect.DeepEqual(request, wantRequest) {
				t.Fatalf("executor request = %#v, want %#v", request, wantRequest)
			}
			return wantResult, nil
		}),
		Limits:           plannerLimits(),
		PlannerTimeout:   time.Second,
		MaxQuestionBytes: 1024,
		QuestionRecorder: recorderFunc(func(_ context.Context, value int64) {
			calls = append(calls, "record-question-bytes")
			if value != int64(len(input.Question)) {
				t.Fatalf("question bytes = %d, want %d", value, len(input.Question))
			}
		}),
	}

	execution, err := service.Ask(context.Background(), input)
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if want := []string{"record-question-bytes", "model", "executor"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if !reflect.DeepEqual(execution.Request, wantRequest) || !reflect.DeepEqual(execution.Result, wantResult) {
		t.Fatalf("execution = %#v, want normalized request and deterministic result", execution)
	}
	if execution.ReferenceTime.Location() != time.UTC || !execution.ReferenceTime.Equal(time.Date(2026, time.July, 29, 16, 0, 0, 123456000, time.UTC)) {
		t.Fatalf("execution reference time = %s, want canonical UTC time", execution.ReferenceTime)
	}
}

func TestServiceAskResultDoesNotRetainExecutorOwnedSlices(t *testing.T) {
	request, err := composeRequest(
		[]byte(executablePointProposal),
		[]identity.EntityID{"entity-atlas-001"},
		plannerLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	executorResult := syntheticPointResult(t, request)
	service := validService(
		modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
			return syntheticModelResponse([]byte(executablePointProposal)), nil
		}),
		executorFunc(func(context.Context, query.Request) (query.Result, error) {
			return executorResult, nil
		}),
	)

	execution, err := service.Ask(context.Background(), serviceInput())
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	entityIDs := append([]identity.EntityID(nil), execution.Result.EntityIDs...)
	predicates := append([]observation.Predicate(nil), execution.Result.Predicates...)
	executorResult.EntityIDs[0] = "entity-mutated-after-execution"
	executorResult.Predicates[0] = "changed_to"

	if !reflect.DeepEqual(execution.Result.EntityIDs, entityIDs) ||
		!reflect.DeepEqual(execution.Result.Predicates, predicates) {
		t.Fatalf("execution result retained executor-owned slices: %#v", execution.Result)
	}
}

func TestServiceAskDoesNotExecuteAfterPlanningFailures(t *testing.T) {
	tests := []struct {
		name  string
		model modelFunc
	}{
		{
			name: "model error",
			model: func(context.Context, ModelRequest) (ModelResponse, error) {
				return ModelResponse{}, errors.New("bounded provider failure")
			},
		},
		{
			name: "cannot plan",
			model: func(context.Context, ModelRequest) (ModelResponse, error) {
				return syntheticModelResponse([]byte(cannotPlanProposal)), nil
			},
		},
		{
			name: "invalid proposal",
			model: func(context.Context, ModelRequest) (ModelResponse, error) {
				return syntheticModelResponse([]byte(`{"status":"not-executable"}`)), nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executorCalls := 0
			service := validService(test.model, executorFunc(func(context.Context, query.Request) (query.Result, error) {
				executorCalls++
				return query.Result{}, nil
			}))
			_, err := service.Ask(context.Background(), serviceInput())
			if err == nil {
				t.Fatal("Ask() error = nil")
			}
			if executorCalls != 0 {
				t.Fatalf("executor calls = %d, want 0", executorCalls)
			}
		})
	}
}

func TestServiceAskRejectsInvalidModelResponsesBeforeExecution(t *testing.T) {
	valid := syntheticModelResponse([]byte(executablePointProposal))
	tests := []struct {
		name     string
		response ModelResponse
	}{
		{"provider", func() ModelResponse { value := valid; value.Provider = "invalid"; return value }()},
		{"model ID", func() ModelResponse { value := valid; value.ModelID = " "; return value }()},
		{"prompt version", func() ModelResponse { value := valid; value.PromptVersion = "other"; return value }()},
		{"schema name", func() ModelResponse { value := valid; value.SchemaName = "other"; return value }()},
		{"attempts", func() ModelResponse { value := valid; value.Attempts = 0; return value }()},
		{"usage", func() ModelResponse { value := valid; value.Usage.TotalTokens = 1; return value }()},
		{"wall latency", func() ModelResponse { value := valid; value.WallLatency = -time.Millisecond; return value }()},
		{"provider latency", func() ModelResponse { value := valid; value.ProviderLatency = -time.Millisecond; return value }()},
		{"output", func() ModelResponse { value := valid; value.Output = []byte(`{`); return value }()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executorCalls := 0
			service := validService(modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
				return test.response, nil
			}), executorFunc(func(context.Context, query.Request) (query.Result, error) {
				executorCalls++
				return query.Result{}, nil
			}))
			_, err := service.Ask(context.Background(), serviceInput())
			if err == nil || err.Error() != "query planner response is invalid" {
				t.Fatalf("Ask() error = %v, want bounded invalid response error", err)
			}
			if executorCalls != 0 {
				t.Fatalf("executor calls = %d, want 0", executorCalls)
			}
		})
	}
}

func TestServiceAskRejectsResultRequestMismatchWithoutExecution(t *testing.T) {
	tests := []struct {
		name   string
		output string
		mutate func(*query.Request)
	}{
		{
			name: "entity IDs", output: executablePointProposal,
			mutate: func(request *query.Request) { request.EntityIDs = []identity.EntityID{"entity-other-001"} },
		},
		{
			name: "entity match", output: executablePointProposal,
			mutate: func(request *query.Request) { request.EntityMatch = query.EntityMatchAny },
		},
		{
			name: "predicates", output: executablePointProposal,
			mutate: func(request *query.Request) { request.Predicates = []observation.Predicate{"changed_to"} },
		},
		{
			name: "selections", output: executablePointProposal,
			mutate: func(request *query.Request) {
				selection, err := temporal.At("point", time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC))
				if err != nil {
					t.Fatal(err)
				}
				request.Selections = []temporal.TemporalSelection{selection}
			},
		},
		{
			name: "knowledge scope", output: executablePointProposal,
			mutate: func(request *query.Request) {
				scope, err := temporal.KnownAsOf(time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC))
				if err != nil {
					t.Fatal(err)
				}
				request.KnowledgeScope = scope
			},
		},
		{
			name: "limit", output: executableTrajectoryProposal,
			mutate: func(request *query.Request) { request.Limit = 3 },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := composeRequest([]byte(test.output), []identity.EntityID{"entity-atlas-001"}, plannerLimits())
			if err != nil {
				t.Fatal(err)
			}
			mismatched := request
			test.mutate(&mismatched)
			service := validService(modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
				return syntheticModelResponse([]byte(test.output)), nil
			}), executorFunc(func(context.Context, query.Request) (query.Result, error) {
				return syntheticResult(t, mismatched), nil
			}))

			execution, err := service.Ask(context.Background(), serviceInput())
			if err == nil || err.Error() != "query planner result does not match the normalized request" {
				t.Fatalf("Ask() error = %v, want bounded result parity error", err)
			}
			if !reflect.DeepEqual(execution, Execution{}) {
				t.Fatalf("execution = %#v, want no execution", execution)
			}
		})
	}
}

func TestServiceAskRejectsExecutorMutationOfNormalizedRequest(t *testing.T) {
	service := validService(modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
		return syntheticModelResponse([]byte(executablePointProposal)), nil
	}), executorFunc(func(_ context.Context, request query.Request) (query.Result, error) {
		request.EntityIDs[0] = "entity-mutated-by-executor"
		request.Predicates[0] = "changed_to"
		return syntheticPointResult(t, request), nil
	}))

	execution, err := service.Ask(context.Background(), serviceInput())
	if err == nil || err.Error() != "query planner result does not match the normalized request" {
		t.Fatalf("Ask() error = %v, want immutable-request parity error", err)
	}
	if !reflect.DeepEqual(execution, Execution{}) {
		t.Fatalf("execution = %#v, want no execution", execution)
	}
}

func TestServiceAskUsesPlanningDeadlineOnlyForModel(t *testing.T) {
	modelHasDeadline := false
	executorHasDeadline := true
	request, err := composeRequest([]byte(executablePointProposal), []identity.EntityID{"entity-atlas-001"}, plannerLimits())
	if err != nil {
		t.Fatal(err)
	}
	service := validService(modelFunc(func(ctx context.Context, _ ModelRequest) (ModelResponse, error) {
		_, modelHasDeadline = ctx.Deadline()
		return syntheticModelResponse([]byte(executablePointProposal)), nil
	}), executorFunc(func(ctx context.Context, _ query.Request) (query.Result, error) {
		_, executorHasDeadline = ctx.Deadline()
		return syntheticPointResult(t, request), nil
	}))

	if _, err := service.Ask(context.Background(), serviceInput()); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if !modelHasDeadline {
		t.Fatal("model context had no planner deadline")
	}
	if executorHasDeadline {
		t.Fatal("executor inherited the planner deadline")
	}
}

func TestServiceAskGivesCallerCancellationPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		stage    string
		context  func() context.Context
		provider error
		want     error
	}{
		{"model caller canceled", "model", canceledContext, context.DeadlineExceeded, context.Canceled},
		{"model caller deadline", "model", expiredContext, context.Canceled, context.DeadlineExceeded},
		{"executor caller canceled", "executor", canceledContext, context.DeadlineExceeded, context.Canceled},
		{"executor caller deadline", "executor", expiredContext, context.Canceled, context.DeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := composeRequest([]byte(executablePointProposal), []identity.EntityID{"entity-atlas-001"}, plannerLimits())
			if err != nil {
				t.Fatal(err)
			}
			model := modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
				if test.stage == "model" {
					return ModelResponse{}, test.provider
				}
				return syntheticModelResponse([]byte(executablePointProposal)), nil
			})
			executor := executorFunc(func(context.Context, query.Request) (query.Result, error) {
				if test.stage == "executor" {
					return query.Result{}, test.provider
				}
				return syntheticPointResult(t, request), nil
			})
			_, err = validService(model, executor).Ask(test.context(), serviceInput())
			if !errors.Is(err, test.want) || err != test.want {
				t.Fatalf("Ask() error = %v, want caller context error %v", err, test.want)
			}
		})
	}
}

func TestServiceAskWrapsNonContextFailuresWithoutBreakingSentinelIdentity(t *testing.T) {
	modelSentinel := errors.New("bounded model sentinel")
	tests := []struct {
		name    string
		service Service
		want    error
		prefix  string
	}{
		{
			name: "model",
			service: validService(modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
				return ModelResponse{}, modelSentinel
			}), executorFunc(func(context.Context, query.Request) (query.Result, error) {
				t.Fatal("executor called after model error")
				return query.Result{}, nil
			})),
			want: modelSentinel, prefix: "plan temporal query: ",
		},
		{
			name: "executor",
			service: validService(modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
				return syntheticModelResponse([]byte(executablePointProposal)), nil
			}), executorFunc(func(context.Context, query.Request) (query.Result, error) {
				return query.Result{}, query.ErrLimitExceeded
			})),
			want: query.ErrLimitExceeded, prefix: "execute planned temporal query: ",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.service.Ask(context.Background(), serviceInput()); !errors.Is(err, test.want) || !strings.HasPrefix(err.Error(), test.prefix) {
				t.Fatalf("Ask() error = %v, want %q wrapping %v", err, test.prefix, test.want)
			}
		})
	}
}

func TestServiceAskRejectsInvalidDependenciesAndSettingsBeforeDisclosure(t *testing.T) {
	calls := 0
	model := modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) { calls++; return ModelResponse{}, nil })
	executor := executorFunc(func(context.Context, query.Request) (query.Result, error) { calls++; return query.Result{}, nil })
	tests := []struct {
		name    string
		ctx     context.Context
		service Service
	}{
		{"nil context", nil, validService(model, executor)},
		{"nil model", context.Background(), validService(nil, executor)},
		{"nil executor", context.Background(), validService(model, nil)},
		{"invalid limits", context.Background(), func() Service {
			service := validService(model, executor)
			service.Limits = query.Limits{}
			return service
		}()},
		{"short timeout", context.Background(), func() Service {
			service := validService(model, executor)
			service.PlannerTimeout = time.Millisecond
			return service
		}()},
		{"long timeout", context.Background(), func() Service {
			service := validService(model, executor)
			service.PlannerTimeout = 6 * time.Minute
			return service
		}()},
		{"small question bound", context.Background(), func() Service { service := validService(model, executor); service.MaxQuestionBytes = 0; return service }()},
		{"large question bound", context.Background(), func() Service {
			service := validService(model, executor)
			service.MaxQuestionBytes = 64*1024 + 1
			return service
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := calls
			if _, err := test.service.Ask(test.ctx, serviceInput()); err == nil {
				t.Fatal("Ask() error = nil")
			}
			if calls != before {
				t.Fatalf("disclosure calls changed from %d to %d", before, calls)
			}
		})
	}
}

func TestServiceAskSpanAndErrorsExcludePrivatePlannerValues(t *testing.T) {
	markers := []string{
		"private-question-marker", "private-entity-id-marker", "private-raw-proposal-marker",
		"private-predicate-marker", "private-timestamp-marker", "private-citation-marker",
		"private-provider-body-marker", "private-database-url-marker", "private-credential-marker",
	}
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	service := validService(modelFunc(func(_ context.Context, request ModelRequest) (ModelResponse, error) {
		if strings.Contains(request.Input, "private-entity-id-marker") {
			t.Fatal("model request disclosed a canonical entity ID")
		}
		return syntheticModelResponse([]byte(`{"private-raw-proposal-marker":"private-provider-body-marker","predicate":"private-predicate-marker","timestamp":"private-timestamp-marker","citation":"private-citation-marker","database_url":"private-database-url-marker","credential":"private-credential-marker"}`)), nil
	}), executorFunc(func(context.Context, query.Request) (query.Result, error) {
		t.Fatal("executor was called after invalid proposal")
		return query.Result{}, nil
	}))
	service.Tracer = provider.Tracer("test")

	_, err := service.Ask(context.Background(), Input{
		Question:      "private-question-marker",
		EntityIDs:     []identity.EntityID{"private-entity-id-marker"},
		ReferenceTime: serviceReferenceTime(),
	})
	if err == nil {
		t.Fatal("Ask() error = nil")
	}
	if spans := exporter.GetSpans(); len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	} else {
		span := spans[0]
		if span.Name != "stacks.query.plan" {
			t.Fatalf("span name = %q, want stacks.query.plan", span.Name)
		}
		serialized := span.Name + span.Status.Description
		for _, attribute := range span.Attributes {
			serialized += string(attribute.Key) + attribute.Value.String()
		}
		for _, event := range span.Events {
			serialized += event.Name
			for _, attribute := range event.Attributes {
				serialized += string(attribute.Key) + attribute.Value.String()
			}
		}
		for _, marker := range markers {
			if strings.Contains(serialized, marker) || strings.Contains(err.Error(), marker) {
				t.Fatalf("planner telemetry or error leaked %q: span=%q error=%q", marker, serialized, err)
			}
		}
	}
}

func TestServiceAskSuccessfulSpanIsExplicitlyOK(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	request, err := composeRequest([]byte(executablePointProposal), []identity.EntityID{"entity-atlas-001"}, plannerLimits())
	if err != nil {
		t.Fatal(err)
	}
	service := validService(modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
		return syntheticModelResponse([]byte(executablePointProposal)), nil
	}), executorFunc(func(context.Context, query.Request) (query.Result, error) {
		return syntheticPointResult(t, request), nil
	}))
	service.Tracer = provider.Tracer("test")

	if _, err := service.Ask(context.Background(), serviceInput()); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Status.Code != codes.Ok {
		t.Fatalf("spans = %#v, want one successful planner span", spans)
	}
}

func validService(model Model, executor Executor) Service {
	return Service{Model: model, Executor: executor, Limits: plannerLimits(), PlannerTimeout: time.Second, MaxQuestionBytes: 1024}
}

func syntheticModelResponse(output []byte) ModelResponse {
	return ModelResponse{
		Output: output, Provider: modelpolicy.ProviderBedrock, ModelID: "synthetic-planner-model",
		PromptVersion: PromptVersion, SchemaName: SchemaName,
		Usage: Usage{InputTokens: 5, OutputTokens: 7, TotalTokens: 12}, Attempts: 1,
		WallLatency: 10 * time.Millisecond, ProviderLatency: 7 * time.Millisecond,
	}
}

func serviceReferenceTime() time.Time {
	return time.Date(2026, time.July, 29, 16, 0, 0, 0, time.UTC)
}

func serviceInput() Input {
	return Input{Question: "What was assigned?", EntityIDs: []identity.EntityID{"entity-atlas-001"}, ReferenceTime: serviceReferenceTime()}
}

func syntheticPointResult(t *testing.T, request query.Request) query.Result {
	t.Helper()
	payload, err := query.NewPointPayload(query.PointInTimeResult{Selection: request.Selections[0], Facts: []query.Fact{}, Unresolved: []query.UnresolvedItem{}})
	if err != nil {
		t.Fatal(err)
	}
	return query.Result{
		Intent: request.Intent, EntityIDs: append([]identity.EntityID(nil), request.EntityIDs...), EntityMatch: request.EntityMatch,
		Predicates: append([]observation.Predicate(nil), request.Predicates...), Selections: append([]temporal.TemporalSelection(nil), request.Selections...),
		KnowledgeScope: request.KnowledgeScope, Limit: request.Limit, Payload: payload, Gaps: []query.Gap{},
	}
}

func syntheticResult(t *testing.T, request query.Request) query.Result {
	t.Helper()
	if request.Intent == temporal.IntentPointInTime {
		return syntheticPointResult(t, request)
	}
	if request.Intent != temporal.IntentTrajectory {
		t.Fatalf("unsupported synthetic result intent %q", request.Intent)
	}
	payload, err := query.NewTrajectoryPayload(query.TrajectoryResult{
		Selection: request.Selections[0], Transitions: []query.Transition{}, Unresolved: []query.UnresolvedItem{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return query.Result{
		Intent: request.Intent, EntityIDs: append([]identity.EntityID(nil), request.EntityIDs...), EntityMatch: request.EntityMatch,
		Predicates: append([]observation.Predicate(nil), request.Predicates...), Selections: append([]temporal.TemporalSelection(nil), request.Selections...),
		KnowledgeScope: request.KnowledgeScope, Limit: request.Limit, Payload: payload, Gaps: []query.Gap{},
	}
}

func benchmarkPointResult(
	b testing.TB,
	request query.Request,
	factCount int,
) query.Result {
	b.Helper()
	validTime, err := observation.AtTime(
		time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		b.Fatal(err)
	}
	facts := make([]query.Fact, factCount)
	for index := range facts {
		suffix := strconv.Itoa(index)
		subject, err := observation.NewEntityTerm("entity-benchmark-"+suffix, "")
		if err != nil {
			b.Fatal(err)
		}
		key, err := temporal.NewStateKey(subject, "assigned_to")
		if err != nil {
			b.Fatal(err)
		}
		value, err := observation.NewTextTerm("value-" + suffix)
		if err != nil {
			b.Fatal(err)
		}
		facts[index] = query.Fact{
			Key:   key,
			Value: value,
			Contributions: []query.Contribution{{
				ObservationID: observation.ObservationID("observation-benchmark-" + suffix),
				Status:        observation.StatusObserved,
				ValidTime:     validTime,
				RecordedAt:    time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
				Derivation:    observation.Derivation{Method: "synthetic", Version: "v1"},
			}},
			SupportingCitations: []query.Citation{{
				EvidenceID:        evidence.EvidenceID("evidence-benchmark-" + suffix),
				Role:              observation.EvidenceSupporting,
				SourceDocumentID:  "document",
				DocumentVersionID: "version",
				SectionID:         "section",
				SectionTitle:      "section",
				SectionPath:       []string{},
				SectionRole:       "body",
				EndOffset:         1,
			}},
			ContradictingCitations: []query.Citation{},
		}
	}
	payload, err := query.NewPointPayload(query.PointInTimeResult{
		Selection:  request.Selections[0],
		Facts:      facts,
		Unresolved: []query.UnresolvedItem{},
	})
	if err != nil {
		b.Fatal(err)
	}
	return query.Result{
		Intent: request.Intent, EntityIDs: append([]identity.EntityID(nil), request.EntityIDs...), EntityMatch: request.EntityMatch,
		Predicates: append([]observation.Predicate(nil), request.Predicates...), Selections: append([]temporal.TemporalSelection(nil), request.Selections...),
		KnowledgeScope: request.KnowledgeScope, Limit: request.Limit, Payload: payload, Gaps: []query.Gap{},
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func expiredContext() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	cancel()
	return ctx
}
