// Command mcp is the composition root for the Fulfillment Execution MCP
// server: it wires env config to outbound adapters, adapters to the read use
// cases, and those to the inbound MCP adapter, then serves MCP over Streamable
// HTTP. It is a second, independent deployable alongside cmd/execution (the
// HTTP service), per ADR-0008.
//
// Auth is a static bearer key (no IdP): set MCP_READ_KEY (and optionally
// MCP_READWRITE_KEY) from a Kubernetes Secret. A request must present a valid
// key; the scope it grants gates the tools.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	inboundmcp "github.com/claudioed/fulfillment-execution/internal/adapters/inbound/mcp"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/events"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/postgres"
	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	"github.com/claudioed/fulfillment-execution/internal/observability"
)

func main() {
	if err := run(); err != nil {
		slog.Error("mcp server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	// Same non-blocking telemetry setup as the HTTP service: an unreachable
	// Collector degrades to dropped telemetry, never a server that won't start.
	rootCtx := context.Background()
	serviceName := getenv("OTEL_SERVICE_NAME", "fulfillment-execution-mcp")
	otelShutdown, err := observability.Setup(rootCtx, serviceName, observability.ServiceVersion(), observability.Endpoint())
	if err != nil {
		logger.Error("opentelemetry setup degraded", "error", err)
	}
	if otelShutdown != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := otelShutdown(ctx); err != nil {
				logger.Error("opentelemetry shutdown failed", "error", err)
			}
		}()
	} else {
		logger.Warn("opentelemetry disabled; traces and metrics will not be exported")
	}

	httpAddr := getenv("MCP_ADDR", ":8090")
	databaseURL := os.Getenv("DATABASE_URL")

	var taskRepo ports.TaskRepo
	if databaseURL == "" {
		logger.Info("database url not configured; using in-memory adapters")
		taskRepo = memory.NewTaskRepo()
	} else {
		if err := postgres.Migrate(databaseURL, "migrations"); err != nil {
			return err
		}
		pool, err := postgres.NewPool(rootCtx, databaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		taskRepo = postgres.NewTaskRepo(pool)
	}

	// The MCP adapter reuses the SAME use cases the HTTP adapter uses:
	// GetQueueDepth (read) and CompleteTask (write), plus the read-only query
	// port satisfied by the same TaskRepo. CompleteTask needs a publisher and
	// clock; the MCP server is not the platform's primary event publisher
	// (cmd/execution is), so it logs the TaskCompleted event rather than
	// publishing to Kafka.
	publisher := events.NewLogPublisher(logger)
	clock := memory.SystemClock{}
	deps := inboundmcp.Deps{
		GetQueueDepth: &usecases.GetQueueDepth{Tasks: taskRepo},
		CompleteTask:  &usecases.CompleteTask{Tasks: taskRepo, Publisher: publisher, Clock: clock},
		Tasks:         taskRepo,
		Now:           time.Now,
	}
	// The curated throughput data-product tool talks to the reports REST
	// service (never the analytical DB directly). It is exposed only when
	// REPORTS_BASE_URL is set, so an MCP deployment without the reports
	// service simply omits the tool.
	if base := os.Getenv("REPORTS_BASE_URL"); base != "" {
		logger.Info("fulfillment reports tool enabled", "reports_base_url", base)
		deps.Reports = inboundmcp.NewReportsRESTClient(base, nil)
	}
	server := inboundmcp.NewServer(deps)

	auth := inboundmcp.NewStaticKeyAuth(authKeys(logger))
	handler := inboundmcp.Handler(server, auth)

	srv := &http.Server{Addr: httpAddr, Handler: handler}

	go func() {
		logger.Info("mcp server listening (Streamable HTTP)", "addr", httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("mcp server failed", "error", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

// authKeys reads the bearer keys from the environment. MCP_READ_KEY grants
// read scope; MCP_READWRITE_KEY grants read-write. If neither is set the server
// still starts but rejects every request (fail closed) — a missing key must
// never mean "open to everyone". The keys themselves are never logged.
func authKeys(logger *slog.Logger) map[string]inboundmcp.Scope {
	keys := make(map[string]inboundmcp.Scope)
	if k := os.Getenv("MCP_READ_KEY"); k != "" {
		keys[k] = inboundmcp.ScopeRead
	}
	if k := os.Getenv("MCP_READWRITE_KEY"); k != "" {
		keys[k] = inboundmcp.ScopeReadWrite
	}
	if len(keys) == 0 {
		logger.Warn("no MCP_READ_KEY or MCP_READWRITE_KEY set; server will reject all requests")
	}
	return keys
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(observability.NewSlogHandler(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}),
	))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
