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

## Why `log/slog`

- **Zero new dependency**: it ships in the Go standard library (1.21+), so
  there's nothing extra to vet, pin, or upgrade.
- **Ecosystem standard**: per current (2026) Go logging comparisons, `slog`
  is the default choice for new services unless you have a specific
  throughput or feature need; `zerolog` and `zap` are faster but that speed
  is not needed at this study-project's throughput, and each pulls in an
  external dependency this codebase doesn't otherwise require.
- **Observability on-ramp**: `slog` has an official OpenTelemetry bridge
  (`otelslog`), so wiring these logs into a tracing/metrics pipeline later
  is a drop-in swap of the handler, not a rewrite of every call site.
