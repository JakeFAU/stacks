package observability

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel"
	otlploggrpc "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	otlpmetricgrpc "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otlptracegrpc "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	apiTrace "go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"stacks/internal/config"
)

const instrumentationName = "stacks"

var (
	durationHistogramBoundaries   = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}
	sizeHistogramBoundaries       = []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 4096, 16384, 65536, 262144, 1048576}
	confidenceHistogramBoundaries = []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1}
)

// Runtime owns process-wide logging and telemetry providers.
type Runtime struct {
	logger         *zap.Logger
	tracerProvider apiTrace.TracerProvider
	meterProvider  metric.MeterProvider
	logProvider    *log.LoggerProvider
	sdkMeter       *sdkmetric.MeterProvider
	sdkTracer      *trace.TracerProvider
}

// New constructs logging and, when enabled, OTLP exporters for all three
// signals. No network connection is required during construction.
func New(ctx context.Context, settings config.Settings) (*Runtime, error) {
	stdoutCore, err := newStdoutCore(settings.LogLevel)
	if err != nil {
		return nil, err
	}

	runtime := &Runtime{
		tracerProvider: tracenoop.NewTracerProvider(),
		meterProvider:  metricnoop.NewMeterProvider(),
	}
	if !settings.Telemetry.Enabled {
		runtime.logger = newLogger(stdoutCore)
		return runtime, nil
	}

	res, err := resource.New(
		ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(semconv.ServiceName(settings.Telemetry.ServiceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("create OpenTelemetry resource: %w", err)
	}

	traceOptions := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(settings.Telemetry.Endpoint)}
	metricOptions := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(settings.Telemetry.Endpoint)}
	logOptions := []otlploggrpc.Option{otlploggrpc.WithEndpoint(settings.Telemetry.Endpoint)}
	if settings.Telemetry.Insecure {
		traceOptions = append(traceOptions, otlptracegrpc.WithInsecure())
		metricOptions = append(metricOptions, otlpmetricgrpc.WithInsecure())
		logOptions = append(logOptions, otlploggrpc.WithInsecure())
	}

	traceExporter, err := otlptracegrpc.New(ctx, traceOptions...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	runtime.sdkTracer = trace.NewTracerProvider(
		trace.WithBatcher(traceExporter),
		trace.WithResource(res),
		trace.WithSampler(trace.ParentBased(trace.TraceIDRatioBased(settings.Telemetry.TraceSampleRatio))),
	)
	runtime.tracerProvider = runtime.sdkTracer

	metricExporter, err := otlpmetricgrpc.New(ctx, metricOptions...)
	if err != nil {
		_ = runtime.sdkTracer.Shutdown(ctx)
		return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}
	runtime.sdkMeter = sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			metricExporter,
			sdkmetric.WithInterval(settings.Telemetry.MetricExportInterval),
		)),
		sdkmetric.WithResource(res),
		sdkmetric.WithView(histogramView("s", durationHistogramBoundaries)),
		sdkmetric.WithView(histogramView("By", sizeHistogramBoundaries)),
		sdkmetric.WithView(histogramView("{item}", sizeHistogramBoundaries)),
		sdkmetric.WithView(histogramView("1", confidenceHistogramBoundaries)),
	)
	runtime.meterProvider = runtime.sdkMeter

	logExporter, err := otlploggrpc.New(ctx, logOptions...)
	if err != nil {
		_ = runtime.sdkMeter.Shutdown(ctx)
		_ = runtime.sdkTracer.Shutdown(ctx)
		return nil, fmt.Errorf("create OTLP log exporter: %w", err)
	}
	runtime.logProvider = log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(logExporter)),
		log.WithResource(res),
	)

	otelCore := otelzap.NewCore(
		instrumentationName,
		otelzap.WithLoggerProvider(runtime.logProvider),
	)
	runtime.logger = newLogger(zapcore.NewTee(stdoutCore, otelCore))

	otel.SetTracerProvider(runtime.sdkTracer)
	otel.SetMeterProvider(runtime.sdkMeter)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return runtime, nil
}

// Logger returns the process logger.
func (r *Runtime) Logger() *zap.Logger {
	return r.logger
}

// TracerProvider returns the provider to inject into transport instrumentation.
func (r *Runtime) TracerProvider() apiTrace.TracerProvider {
	return r.tracerProvider
}

// MeterProvider returns the provider to inject into transport instrumentation.
func (r *Runtime) MeterProvider() metric.MeterProvider {
	return r.meterProvider
}

// DecisionRecorder builds the shared event and distribution recorder used at
// decision boundaries.
func (r *Runtime) DecisionRecorder() (*DecisionRecorder, error) {
	return NewDecisionRecorder(r.meterProvider.Meter(instrumentationName))
}

// Shutdown flushes signals in dependency order.
func (r *Runtime) Shutdown(ctx context.Context) error {
	var shutdownErrors []error
	if r.logProvider != nil {
		shutdownErrors = append(shutdownErrors, r.logProvider.Shutdown(ctx))
	}
	if r.sdkMeter != nil {
		shutdownErrors = append(shutdownErrors, r.sdkMeter.Shutdown(ctx))
	}
	if r.sdkTracer != nil {
		shutdownErrors = append(shutdownErrors, r.sdkTracer.Shutdown(ctx))
	}
	return errors.Join(shutdownErrors...)
}

func newStdoutCore(levelName string) (zapcore.Core, error) {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(levelName)); err != nil {
		return nil, fmt.Errorf("parse log level %q: %w", levelName, err)
	}

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	return zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.Lock(os.Stdout),
		level,
	), nil
}

func newLogger(core zapcore.Core) *zap.Logger {
	return zap.New(
		core,
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel),
	)
}

func histogramView(unit string, boundaries []float64) sdkmetric.View {
	return sdkmetric.NewView(
		sdkmetric.Instrument{Kind: sdkmetric.InstrumentKindHistogram, Unit: unit},
		sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
			Boundaries: boundaries,
			NoMinMax:   false,
		}},
	)
}
