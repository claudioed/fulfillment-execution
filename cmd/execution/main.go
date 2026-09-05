// Command execution is the composition root for the Fulfillment Execution
// service: it wires env config to adapters, adapters to use cases, and use
// cases to the HTTP router.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	inboundhttp "github.com/claudioed/fulfillment-execution/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/fulfillment-execution/internal/adapters/inbound/kafka"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/events"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/filecatalog"
	outboundkafka "github.com/claudioed/fulfillment-execution/internal/adapters/outbound/kafka"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/postgres"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/productclassification"
	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/observability"
)

// workReleasedTopic is the topic wes-work-planning publishes WorkReleased
// events to.
const workReleasedTopic = "warehouse.work-planning.events"

func main() {
	if err := run(); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	// Telemetry comes up right after the logger and before any adapter, so
	// everything built below is instrumented. An unreachable Collector is
	// not fatal: the OTLP exporters dial lazily, so the service starts and
	// serves normally with telemetry dropped on the floor.
	rootCtx := context.Background()
	serviceName := observability.ServiceName()
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
	logger.Info("telemetry configured",
		"service_name", serviceName,
		"service_version", observability.ServiceVersion(),
		"otlp_endpoint", observability.Endpoint(),
	)

	metrics, err := observability.NewMetrics()
	if err != nil {
		// A missing counter must not stop the service from doing work.
		logger.Error("task metrics unavailable", "error", err)
		metrics = nil
	}

	httpAddr := getenv("HTTP_ADDR", ":8080")
	databaseURL := os.Getenv("DATABASE_URL")
	kafkaBrokers := strings.Split(getenv("KAFKA_BROKERS", "localhost:9092"), ",")

	// The process-path catalogue is loaded and validated once at boot,
	// before anything else stands up — a missing or malformed catalogue
	// file must stop this service from starting at all, never fall back
	// to a partial/empty catalogue (see filecatalog.Load's doc comment).
	catalogue, err := filecatalog.Load(getenv("PATH_CATALOGUE_FILE", "/etc/fulfillment-execution/process-paths.yaml"))
	if err != nil {
		return fmt.Errorf("failed to load the process-path catalogue: %w", err)
	}
	logger.Info("process-path catalogue loaded", "paths", catalogue.Ids())

	var (
		taskRepo          ports.TaskRepo
		stationRepo       ports.StationRepo
		packageRepo       ports.PackageRepo
		processedEvents   ports.ProcessedEvents
		consolidationRepo ports.OrderConsolidationRepo
	)

	if databaseURL == "" {
		logger.Info("database url not configured; using in-memory adapters")
		taskRepo = memory.NewTaskRepo()
		stationRepo = memory.NewStationRepo()
		packageRepo = memory.NewPackageRepo()
		processedEvents = memory.NewProcessedEventsRepo()
		consolidationRepo = memory.NewOrderConsolidationRepo()
	} else {
		if err := postgres.Migrate(databaseURL, "migrations"); err != nil {
			return err
		}
		pool, err := postgres.NewPool(rootCtx, databaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		if err := postgres.RecordPoolStats(pool); err != nil {
			logger.Error("pgxpool metrics unavailable", "error", err)
		}
		taskRepo = postgres.NewTaskRepo(pool)
		stationRepo = postgres.NewStationRepo(pool)
		packageRepo = postgres.NewPackageRepo(pool)
		processedEvents = postgres.NewProcessedEventsRepo(pool)
		consolidationRepo = postgres.NewOrderConsolidationRepo(pool)
	}

	var (
		publisher      ports.EventPublisher
		kafkaPublisher *outboundkafka.Publisher
		analyticsPub   *outboundkafka.AnalyticsPublisher
	)
	if getenv("EVENT_PUBLISHER", "log") == "kafka" {
		logger.Info("event publisher configured", "publisher", "kafka", "topic", outboundkafka.Topic, "analytics_topic", outboundkafka.AnalyticsTopic, "brokers", kafkaBrokers)
		kafkaPublisher = outboundkafka.NewPublisher(kafkaBrokers, taskRepo, stationRepo, uuidLike)
		analyticsPub = outboundkafka.NewAnalyticsPublisher(kafkaBrokers, taskRepo, uuidLike)
		// Fan every domain event to BOTH the integration topic and the
		// dedicated analytics topic (ADR-0012). The analytics stream feeds
		// the projector; the integration stream is unchanged.
		publisher = events.NewMultiPublisher(kafkaPublisher, analyticsPub)
	} else {
		publisher = events.NewLogPublisher(logger)
	}
	clock := memory.SystemClock{}
	classificationLookup := buildClassificationLookup(getenv("PRODUCT_CLASSIFICATION_MODE", "permissive"), os.Getenv("INVENTORY_STORAGE_BASE_URL"), logger)

	createTask := &usecases.CreateTask{Tasks: taskRepo, Publisher: publisher, Clock: clock, NewId: newTaskId}

	handlers := &inboundhttp.Handlers{
		CreateTask:         createTask,
		ClaimNext:          &usecases.ClaimNext{Tasks: taskRepo, Stations: stationRepo, Publisher: publisher, Clock: clock, Metrics: metricsPort(metrics)},
		RenewLease:         &usecases.RenewLease{Tasks: taskRepo, Clock: clock},
		CompleteTask:       &usecases.CompleteTask{Tasks: taskRepo, Publisher: publisher, Clock: clock, Metrics: metricsPort(metrics)},
		SealPackage:        &usecases.SealPackage{Tasks: taskRepo, Packages: packageRepo, Publisher: publisher, Clock: clock, NewId: newPackageId, ClassificationLookup: classificationLookup},
		RunSlam:            &usecases.RunSlam{Packages: packageRepo, Publisher: publisher, Clock: clock},
		GetQueueDepth:      &usecases.GetQueueDepth{Tasks: taskRepo},
		ExpireLeases:       &usecases.ExpireLeases{Tasks: taskRepo, Publisher: publisher, Clock: clock},
		RegisterStation:    &usecases.RegisterStation{Stations: stationRepo, Publisher: publisher},
		GetTasksByOrderRef: &usecases.GetTasksByOrderRef{Tasks: taskRepo},
		CheckInStation:     &usecases.CheckInStation{Stations: stationRepo},
		CheckOutStation:    &usecases.CheckOutStation{Stations: stationRepo},
		ArriveAtRebin: &usecases.ArriveAtRebin{
			Consolidations: consolidationRepo,
			CreateTask:     createTask,
			Publisher:      publisher,
			Clock:          clock,
		},
		GetInstalledCapacity: &usecases.GetInstalledCapacity{Stations: stationRepo},
	}
	router := inboundhttp.NewRouter(handlers, logger)

	srv := &http.Server{Addr: httpAddr, Handler: router, ReadHeaderTimeout: 5 * time.Second}

	consumerGroup := getenv("WORK_RELEASED_CONSUMER_GROUP", "fulfillment-execution")
	consumer := inboundkafka.NewConsumerWithGroup(kafkaBrokers, workReleasedTopic, consumerGroup, createTask, processedEvents, catalogue, logger)
	defer func() { _ = consumer.Close() }()
	if kafkaPublisher != nil {
		defer func() { _ = kafkaPublisher.Close() }()
	}
	if analyticsPub != nil {
		defer func() { _ = analyticsPub.Close() }()
	}

	go func() {
		logger.Info("http server listening", "addr", httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "error", err)
		}
	}()

	consumerCtx, cancelConsumer := context.WithCancel(context.Background())
	go func() {
		logger.Info("kafka consumer starting", "topic", workReleasedTopic, "brokers", kafkaBrokers)
		if err := consumer.Run(consumerCtx); err != nil {
			logger.Error("kafka consumer stopped", "error", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	cancelConsumer()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

// metricsPort converts a possibly-nil *observability.Metrics into a
// ports.Metrics, avoiding the typed-nil trap: assigning a nil *Metrics
// straight into the interface field would produce a non-nil interface whose
// method calls panic, defeating the use cases' nil check.
func metricsPort(m *observability.Metrics) ports.Metrics {
	if m == nil {
		return nil
	}
	return m
}

// newLogger builds a JSON slog.Logger writing to stdout, with its minimum
// level set from a LOG_LEVEL value (debug|info|warn|warning|error,
// case-insensitive, defaulting to info for anything else). The JSON handler
// is wrapped so records logged with a context carrying an active span also
// carry that span's trace_id and span_id.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
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

// buildClassificationLookup selects the outbound
// ports.ProductClassificationLookup adapter via PRODUCT_CLASSIFICATION_MODE
// (http|permissive), defaulting to "permissive" so existing tests, CI and
// deployments that do not set the env var are unaffected — mirrors
// inventory-storage's own LOCATION_LOOKUP_MODE=http|permissive pattern for
// its facilitylayout adapter (see ADR-0010). "http" requires
// INVENTORY_STORAGE_BASE_URL.
func buildClassificationLookup(mode, inventoryStorageBaseURL string, logger *slog.Logger) ports.ProductClassificationLookup {
	if !strings.EqualFold(mode, "http") {
		return productclassification.NewPermissiveLookup()
	}
	logger.Info("product classification lookup configured", "mode", "http", "inventory_storage_base_url", inventoryStorageBaseURL)
	return productclassification.NewClient(inventoryStorageBaseURL, nil)
}

func newTaskId() shared.TaskId {
	return shared.TaskId(uuidLike())
}

func newPackageId() shared.PackageId {
	return shared.PackageId(uuidLike())
}

// uuidLike generates a time-ordered, sufficiently-unique id without pulling
// in an external UUID dependency.
func uuidLike() string {
	return time.Now().UTC().Format("20060102T150405.000000000")
}
