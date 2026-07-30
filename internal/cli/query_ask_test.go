package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/identity"
	"stacks/internal/modelpolicy"
	"stacks/internal/query"
	"stacks/internal/queryplan"
)

func TestQueryAskRejectsInvalidLocalInputBeforeFactory(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		input      string
		invocation Invocation
		limits     query.Limits
	}{
		{"empty", "", validQueryAskInvocation(), queryAskLimits()},
		{"whitespace", " \n\t", validQueryAskInvocation(), queryAskLimits()},
		{"invalid utf8", string([]byte{0xff}), validQueryAskInvocation(), queryAskLimits()},
		{"oversize", "longer than five", validQueryAskInvocation(), queryAskLimits()},
		{"contains id", "entity-atlas-001 is private", validQueryAskInvocation(), queryAskLimits()},
		{"invalid ids", "question", Invocation{Command: CommandQuery, Action: ActionAsk, QueryAsk: &QueryAskInput{}}, queryAskLimits()},
		{"invalid limits", "question", validQueryAskInvocation(), query.Limits{}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			factoryCalls := 0
			command := QueryAskCommand{
				Input: strings.NewReader(testCase.input), Output: io.Discard, Limits: testCase.limits,
				MaxQuestionBytes: 5,
				NewService: func(context.Context) (QueryAskService, error) {
					factoryCalls++
					return queryAskServiceFunc(func(context.Context, queryplan.Input) (queryplan.Execution, error) {
						return queryplan.Execution{}, nil
					}), nil
				},
			}
			if err := command.Run(t.Context(), testCase.invocation); err == nil {
				t.Fatal("Run() error = nil, want local input rejection")
			}
			if factoryCalls != 0 {
				t.Fatalf("factory calls = %d, want 0", factoryCalls)
			}
		})
	}
}

func TestQueryAskReadsAtMostConfiguredMaximumPlusOne(t *testing.T) {
	reader := &readTrackingReader{payload: "0123456789"}
	err := (QueryAskCommand{Input: reader, Limits: queryAskLimits(), MaxQuestionBytes: 5}).Run(t.Context(), validQueryAskInvocation())
	if err == nil {
		t.Fatal("Run() error = nil, want oversize rejection")
	}
	if reader.maximum > 6 {
		t.Fatalf("maximum read = %d, want at most 6", reader.maximum)
	}
}

func TestQueryAskCallsFactoryAndServiceOnceAfterNormalization(t *testing.T) {
	execution := validQueryAskExecution(t)
	factoryCalls := 0
	service := &recordingQueryAskService{execution: execution}
	var output bytes.Buffer
	err := (QueryAskCommand{
		Input: strings.NewReader("What was assigned?"), Output: &output,
		Limits: queryAskLimits(), MaxQuestionBytes: 1024,
		NewService: func(context.Context) (QueryAskService, error) {
			factoryCalls++
			return service, nil
		},
	}).Run(t.Context(), validQueryAskInvocation())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if factoryCalls != 1 || service.calls != 1 {
		t.Fatalf("factory/service calls = %d/%d, want 1/1", factoryCalls, service.calls)
	}
	if output.Len() == 0 {
		t.Fatal("output is empty")
	}
}

func TestQueryAskDoesNotWriteOnServiceOrRendererFailure(t *testing.T) {
	const privateFailure = "synthetic-private-model-detail"
	for _, testCase := range []struct {
		name      string
		execution queryplan.Execution
		err       error
	}{
		{"service", queryplan.Execution{}, errors.New(privateFailure)},
		{"renderer", queryplan.Execution{}, nil},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var output bytes.Buffer
			execution := testCase.execution
			if testCase.name == "renderer" {
				execution = validQueryAskExecution(t)
				execution.SchemaVersion = "wrong"
			}
			err := (QueryAskCommand{
				Input: strings.NewReader("What was assigned?"), Output: &output,
				Limits: queryAskLimits(), MaxQuestionBytes: 1024,
				NewService: func(context.Context) (QueryAskService, error) {
					return &recordingQueryAskService{execution: execution, err: testCase.err}, nil
				},
			}).Run(t.Context(), validQueryAskInvocation())
			if err == nil {
				t.Fatal("Run() error = nil")
			}
			if strings.Contains(err.Error(), privateFailure) {
				t.Fatalf("error exposed private service detail: %q", err)
			}
			if output.Len() != 0 {
				t.Fatalf("output = %q, want none", output.String())
			}
		})
	}
}

func TestQueryAskPreservesShortWrite(t *testing.T) {
	execution := validQueryAskExecution(t)
	err := (QueryAskCommand{
		Input: strings.NewReader("What was assigned?"), Output: shortQueryAskWriter{},
		Limits: queryAskLimits(), MaxQuestionBytes: 1024,
		NewService: func(context.Context) (QueryAskService, error) {
			return &recordingQueryAskService{execution: execution}, nil
		},
	}).Run(t.Context(), validQueryAskInvocation())
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Run() error = %v, want io.ErrShortWrite", err)
	}
}

func TestQueryAskRenderersRejectOverflowingPlannerUsage(t *testing.T) {
	execution := validQueryAskExecution(t)
	execution.Planner.Usage = queryplan.Usage{
		InputTokens:  math.MaxInt64,
		OutputTokens: 1,
		TotalTokens:  math.MaxInt64,
	}
	for name, render := range map[string]func(queryplan.Execution) ([]byte, error){
		"text": renderQueryAskText,
		"json": renderQueryAskJSON,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := render(execution); err == nil {
				t.Fatal("renderer error = nil, want invalid overflowing usage rejection")
			}
		})
	}
}

func TestQueryAskCanonicalizesServiceFactoryCancellationWithoutLeakingDetails(t *testing.T) {
	const privateFactoryDetail = "synthetic-private-factory-detail"
	tests := []struct {
		name       string
		callerErr  error
		factoryErr error
		want       error
	}{
		{
			name:      "caller canceled during factory wins over deadline",
			callerErr: context.Canceled, factoryErr: context.DeadlineExceeded,
			want: context.Canceled,
		},
		{
			name:      "caller deadline during factory wins over cancellation",
			callerErr: context.DeadlineExceeded, factoryErr: context.Canceled,
			want: context.DeadlineExceeded,
		},
		{
			name:       "wrapped factory cancellation",
			factoryErr: fmt.Errorf("%s: %w", privateFactoryDetail, context.Canceled),
			want:       context.Canceled,
		},
		{
			name:       "joined factory deadline",
			factoryErr: errors.Join(errors.New(privateFactoryDetail), context.DeadlineExceeded),
			want:       context.DeadlineExceeded,
		},
		{
			name:       "non cancellation remains bounded",
			factoryErr: errors.New(privateFactoryDetail),
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := newQueryAskControlledContext(t.Context())
			service := &recordingQueryAskService{}
			factoryCalls := 0
			var output bytes.Buffer
			err := (QueryAskCommand{
				Input: strings.NewReader("What was assigned?"), Output: &output,
				Limits: queryAskLimits(), MaxQuestionBytes: 1024,
				NewService: func(context.Context) (QueryAskService, error) {
					factoryCalls++
					ctx.fail(testCase.callerErr)
					return service, testCase.factoryErr
				},
			}).Run(ctx, validQueryAskInvocation())
			if factoryCalls != 1 {
				t.Fatalf("factory calls = %d, want 1", factoryCalls)
			}
			if service.calls != 0 {
				t.Fatalf("service calls = %d, want 0", service.calls)
			}
			if output.Len() != 0 {
				t.Fatalf("stdout = %q, want none", output.String())
			}
			if testCase.want != nil {
				if err != testCase.want {
					t.Fatalf("Run() error = %v, want canonical %v", err, testCase.want)
				}
			} else if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Run() error = %v, want bounded non-cancellation failure", err)
			}
			if strings.Contains(err.Error(), privateFactoryDetail) {
				t.Fatalf("Run() error exposed private factory detail: %q", err)
			}
		})
	}
}

type queryAskServiceFunc func(context.Context, queryplan.Input) (queryplan.Execution, error)

func (fn queryAskServiceFunc) Ask(ctx context.Context, input queryplan.Input) (queryplan.Execution, error) {
	return fn(ctx, input)
}

type recordingQueryAskService struct {
	execution queryplan.Execution
	err       error
	calls     int
}

type queryAskControlledContext struct {
	context.Context
	done chan struct{}
	err  error
}

func newQueryAskControlledContext(parent context.Context) *queryAskControlledContext {
	return &queryAskControlledContext{Context: parent, done: make(chan struct{})}
}

func (ctx *queryAskControlledContext) Done() <-chan struct{} { return ctx.done }
func (ctx *queryAskControlledContext) Err() error            { return ctx.err }

func (ctx *queryAskControlledContext) fail(err error) {
	if err == nil {
		return
	}
	ctx.err = err
	close(ctx.done)
}

func (service *recordingQueryAskService) Ask(_ context.Context, _ queryplan.Input) (queryplan.Execution, error) {
	service.calls++
	return service.execution, service.err
}

type readTrackingReader struct {
	payload string
	offset  int
	reads   int
	maximum int
}

func (reader *readTrackingReader) Read(value []byte) (int, error) {
	reader.reads++
	if len(value) > reader.maximum {
		reader.maximum = len(value)
	}
	if reader.offset >= len(reader.payload) {
		return 0, io.EOF
	}
	n := copy(value, reader.payload[reader.offset:])
	reader.offset += n
	return n, nil
}

type shortQueryAskWriter struct{}

func (shortQueryAskWriter) Write(value []byte) (int, error) { return len(value) - 1, nil }

func validQueryAskInvocation() Invocation {
	return Invocation{Command: CommandQuery, Action: ActionAsk, QueryAsk: &QueryAskInput{
		EntityIDs:     []identity.EntityID{"entity-atlas-001"},
		ReferenceTime: time.Date(2026, time.July, 29, 12, 0, 0, 0, time.FixedZone("east", -4*60*60)),
		Output:        QueryOutputText,
	}}
}

func queryAskLimits() query.Limits {
	return query.Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 20}
}

func validQueryAskExecution(t *testing.T) queryplan.Execution {
	t.Helper()
	result := populatedTrendResult(t, false)
	request := requestFromResult(result)
	return queryplan.Execution{
		SchemaVersion: queryplan.OutputSchemaVersion,
		ReferenceTime: time.Date(2026, time.July, 29, 16, 0, 0, 0, time.UTC),
		Request:       request,
		Planner: queryplan.PlannerMetadata{
			Provider: modelpolicy.ProviderOpenAI, ModelID: "synthetic-planner-model",
			PromptVersion: queryplan.PromptVersion, SchemaName: queryplan.SchemaName,
			Usage: queryplan.Usage{InputTokens: 120, OutputTokens: 40, TotalTokens: 160}, Attempts: 1,
			WallLatency: 250 * time.Millisecond, ProviderLatency: 200 * time.Millisecond,
		},
		Result: result,
	}
}
