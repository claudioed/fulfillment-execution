# Structured Logging

## Library

Fulfillment Execution uses Go's standard library **`log/slog`** (Go 1.21+)
with `slog.NewJSONHandler` for structured, machine-parseable logs. All logs
are emitted as JSON to stdout.

## Configuration

| Env var     | Values                              | Default |
|-------------|--------------------------------------|---------|
| `LOG_LEVEL` | `debug` \| `info` \| `warn` \| `error` (case-insensitive) | `info` |

Any unrecognized value falls back to `info`.

## Where logs come from

- **HTTP layer**: `internal/adapters/inbound/http`'s `RequestLogger`
  middleware logs one line per request (method, path, status, duration,
  bytes written, chi request id). Requests with a 5xx status are logged at
  `Error` level; everything else at `Info`.
- **Event publisher**: `internal/adapters/outbound/events.LogPublisher` logs
  each published domain event at `Info` level.
- **Kafka consumer**: `internal/adapters/inbound/kafka.Consumer` logs
  message-handling failures at `Error` level.
- **Composition root**: `cmd/execution/main.go` builds the process-wide
  `*slog.Logger` from `LOG_LEVEL`, sets it as `slog.Default()`, and passes it
  into the adapters above.
- **OpenTelemetry SDK**: its own errors (a failed export, a bad instrument)
  are routed into this logger at `Warn` level by an `otel.SetErrorHandler`
  installed in `observability.Setup`, so they are JSON like everything else
  rather than plain text on stderr.

## Trace correlation

The JSON handler is wrapped by `observability.NewSlogHandler`
(`internal/observability/slogotel.go`). When a record is logged with a
context that carries an active span, the handler appends `trace_id` and
`span_id`:

```json
{"time":"...","level":"INFO","msg":"http request","method":"POST","path":"/tasks",
 "status":201,"trace_id":"8812c36621d214139a08949823716b93","span_id":"14ddf02dbd8913ba"}
```

This only works through the **context-carrying** flavours of the slog API
(`InfoContext`, `ErrorContext`, ...) — a plain `logger.Info` has no context
to read a span from. The request logger, the log publisher and the Kafka
consumer all use the `*Context` forms for this reason.

See the [Observability section of the README](README.md#observability) for
what else the telemetry pipeline exports.

## Why `log/slog`

- **Zero new dependency**: it ships in the Go standard library (1.21+), so
  there's nothing extra to vet, pin, or upgrade.
- **Ecosystem standard**: per current (2026) Go logging comparisons, `slog`
  is the default choice for new services unless you have a specific
  throughput or feature need; `zerolog` and `zap` are faster but that speed
  is not needed at this study-project's throughput, and each pulls in an
  external dependency this codebase doesn't otherwise require.
- **Observability on-ramp**: `slog`'s handler interface made trace
  correlation a wrapper around the existing JSON handler rather than a
  rewrite of every call site — which is exactly how it was added.
