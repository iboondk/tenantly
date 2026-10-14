# ADR 0005: OpenTelemetry tracing with otelhttp and otelpgx, stdout by default

- **Status:** Accepted

## Context

When a request is slow or fails, we need to see where the time went: the HTTP handler, the Redis calls, and each Postgres query. The service must also run locally without a tracing backend, and it must be able to send traces to a collector in other environments without code changes.

## Decision

- **HTTP spans.** The whole router is wrapped in `otelhttp.NewHandler`, so each request gets a server span. The GraphQL endpoint is inside the same router and gets the same span.
- **Database spans.** The pgx pool is created with `otelpgx.NewTracer()` (`internal/db/pool.go`), so each query gets a span as a child of the request span, when the context carries one.
- **SDK and exporter.** The `go.opentelemetry.io/otel` SDK is set up in `internal/telemetry`. The exporter is chosen by `OTEL_EXPORTER`: `stdout` (the default), `otlp` (gRPC to `OTEL_EXPORTER_OTLP_ENDPOINT`), or `none`. Any other value is rejected at configuration load time.
- **Propagation.** A composite propagator with W3C trace context and baggage is installed globally, so incoming trace headers from other services are continued.
- **Shutdown.** The main function flushes the provider on exit with a short timeout.

## Consequences

- Local runs need no collector. The default `stdout` exporter prints spans to the console, which is enough for development and for checking that spans are created.
- Production or staging can send traces to any OTLP-compatible collector by setting two environment variables.
- Every query gets a span, which adds some overhead. Under load this overhead is part of what the k6 run measures, and the run metadata should say which exporter was used.
- Spans carry request and query details. Secrets, tokens and personal data must not be added to span attributes, and traces sent to an external collector are treated as data leaving the service.

## Alternatives considered

- **Metrics only (Prometheus).** Good for trends, but it does not show where one slow request spent its time.
- **Vendor SDK.** Lock-in to one backend. OpenTelemetry keeps the instrumentation separate from the backend choice.
- **Tracing disabled by default.** Less output locally, but developers would have to enable it to debug, and the default path would be less tested. `stdout` keeps the code path active.
- **OTLP as the default exporter.** Requires a running collector for a plain local run, which the project avoids.
