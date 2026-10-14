// Package telemetry configures the OpenTelemetry tracer provider.
package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Setup installs a global TracerProvider and a W3C trace-context and baggage
// propagator. exporter is one of "stdout", "otlp" or "none". The returned
// function flushes and shuts the provider down.
func Setup(ctx context.Context, exporter, endpoint, serviceName string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if exporter == "none" {
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }, nil
	}

	var (
		exp sdktrace.SpanExporter
		err error
	)
	switch exporter {
	case "stdout":
		exp, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
	case "otlp":
		exp, err = otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(endpoint),
			otlptracegrpc.WithInsecure(),
		)
	default:
		return nil, fmt.Errorf("telemetry: unknown exporter %q", exporter)
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry: create %s exporter: %w", exporter, err)
	}

	res := resource.NewSchemaless(attribute.String("service.name", serviceName))
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}
