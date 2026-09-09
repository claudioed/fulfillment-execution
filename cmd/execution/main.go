// Command execution is the composition root for the Fulfillment Execution
// service: it wires env config to adapters, adapters to use cases, and use
// cases to the HTTP router.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/fulfillment-execution/internal/adapters/inbound/auth"
	inboundhttp "github.com/claudioed/fulfillment-execution/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/fulfillment-execution/internal/adapters/inbound/kafka"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/events"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/filecatalog"
	outboundkafka "github.com/claudioed/fulfillment-execution/internal/adapters/outbound/kafka"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/kafkacatalog"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/postgres"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/productclassification"
	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	"github.com/claudioed/fulfillment-execution/internal/domain/pathcatalog"
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

	// The process-path catalogue's SOURCE is switchable
	// (PATH_CATALOGUE_SOURCE=file|kafka, defaulting to "file" so nothing
	// about existing deployments changes unless explicitly opted in —
	// same additive-and-defaulted-off convention as EVENT_PUBLISHER).
	//
	// "file": the original behavior. Loaded and validated once at boot,
	// before anything else stands up — a missing or malformed catalogue
	// file stops this service from starting at all, never falls back to
	// a partial/empty catalogue (see filecatalog.Load's doc comment).
	//
	// "kafka": consumes process-path-management's published events
	// instead of warehouse-infra's static YAML file (see
	// internal/adapters/outbound/kafkacatalog's package doc comment for
	// the full rationale and how it preserves the same "never serve
	// traffic against an incomplete catalogue" guarantee via a
	// readiness gate rather than a boot-time file read).
	var (
		catalogue      ports.PathCatalogue
		kafkaCatalogue *kafkacatalog.Consumer
	)
	catalogueSource := getenv("PATH_CATALOGUE_SOURCE", "file")
	// consumerCtx/cancelConsumer are declared here (rather than at their
	// original later location) because the Kafka catalogue source needs
	// its consumer loop RUNNING before WaitReady is called below --
	// WaitReady blocks on messages actually being read, so starting the
	// goroutine after the wait would deadlock until WaitReadyTimeout on
	// every single boot.
	consumerCtx, cancelConsumer := context.WithCancel(context.Background())
	defer cancelConsumer()
	switch catalogueSource {
	case "kafka":
		var err error
		kafkaCatalogue, err = kafkacatalog.NewConsumer(rootCtx, kafkaBrokers, logger)
		if err != nil {
			return fmt.Errorf("failed to start the process-path Kafka catalogue: %w", err)
		}
		catalogue = kafkaCatalogue
		logger.Info("process-path catalogue source configured", "source", "kafka", "topic", kafkacatalog.Topic)

		go func() {
			logger.Info("process-path catalogue consumer running", "topic", kafkacatalog.Topic)
			if err := kafkaCatalogue.Run(consumerCtx); err != nil {
				logger.Error("process-path catalogue consumer stopped", "error", err)
			}
		}()

		waitCtx, cancelWait := context.WithTimeout(rootCtx, kafkacatalog.WaitReadyTimeout)
		logger.Info("waiting for the process-path catalogue to replay its initial history before accepting traffic")
		waitErr := kafkaCatalogue.WaitReady(waitCtx)
		cancelWait()
		if waitErr != nil {
			return fmt.Errorf("process-path catalogue did not become ready within %s: %w", kafkacatalog.WaitReadyTimeout, waitErr)
		}
		logger.Info("process-path catalogue is ready", "paths", kafkaCatalogue.Ids())
	default:
		var err error
		catalogue, err = filecatalog.Load(getenv("PATH_CATALOGUE_FILE", "/etc/fulfillment-execution/process-paths.yaml"))
		if err != nil {
			return fmt.Errorf("failed to load the process-path catalogue: %w", err)
		}
		logger.Info("process-path catalogue source configured", "source", "file", "paths", catalogue.(*pathcatalog.Catalogue).Ids())
	}

	var (
		taskRepo          ports.TaskRepo
		stationRepo       ports.StationRepo
		packageRepo       ports.PackageRepo
		processedEvents   ports.ProcessedEvents
		consolidationRepo ports.OrderConsolidationRepo
		// pool and uow are nil when running in-memory: the use cases then
		// run Save and Publish back to back (see ports.UnitOfWork).
		pool *pgxpool.Pool
		uow  ports.UnitOfWork
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
		var err error
		pool, err = postgres.NewPool(rootCtx, databaseURL)
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
		uow = postgres.NewUnitOfWork(pool)
	}

	publisher, relay, closePublisher := buildEventPublisher(pool, kafkaBrokers, taskRepo, stationRepo, logger)
	defer closePublisher()
	clock := memory.SystemClock{}
	classificationLookup := buildClassificationLookup(getenv("PRODUCT_CLASSIFICATION_MODE", "permissive"), os.Getenv("INVENTORY_STORAGE_BASE_URL"), os.Getenv("INVENTORY_STORAGE_API_KEY"), logger)

	createTask := &usecases.CreateTask{Tasks: taskRepo, Publisher: publisher, Clock: clock, NewId: newTaskId, UnitOfWork: uow}

	handlers := &inboundhttp.Handlers{
		CreateTask:         createTask,
		ClaimNext:          &usecases.ClaimNext{Tasks: taskRepo, Stations: stationRepo, Publisher: publisher, Clock: clock, Metrics: metricsPort(metrics), UnitOfWork: uow},
		RenewLease:         &usecases.RenewLease{Tasks: taskRepo, Clock: clock},
		CompleteTask:       &usecases.CompleteTask{Tasks: taskRepo, Publisher: publisher, Clock: clock, Metrics: metricsPort(metrics), UnitOfWork: uow},
		SealPackage:        &usecases.SealPackage{Tasks: taskRepo, Packages: packageRepo, Publisher: publisher, Clock: clock, NewId: newPackageId, ClassificationLookup: classificationLookup, UnitOfWork: uow},
		RunSlam:            &usecases.RunSlam{Packages: packageRepo, Publisher: publisher, Clock: clock, UnitOfWork: uow},
		GetQueueDepth:      &usecases.GetQueueDepth{Tasks: taskRepo},
		ExpireLeases:       &usecases.ExpireLeases{Tasks: taskRepo, Publisher: publisher, Clock: clock, UnitOfWork: uow},
		RegisterStation:    &usecases.RegisterStation{Stations: stationRepo, Publisher: publisher},
		GetTasksByOrderRef: &usecases.GetTasksByOrderRef{Tasks: taskRepo},
		CheckInStation:     &usecases.CheckInStation{Stations: stationRepo},
		CheckOutStation:    &usecases.CheckOutStation{Stations: stationRepo},
		ArriveAtRebin: &usecases.ArriveAtRebin{
			Consolidations: consolidationRepo,
			CreateTask:     createTask,
			Publisher:      publisher,
			Clock:          clock,
			UnitOfWork:     uow,
		},
		GetInstalledCapacity: &usecases.GetInstalledCapacity{Stations: stationRepo},
	}
	authn, authMode := buildAuth(logger)
	router := inboundhttp.NewRouter(handlers, logger, inboundhttp.WithAuth(authn, authMode))

	srv := &http.Server{Addr: httpAddr, Handler: router, ReadHeaderTimeout: 5 * time.Second}

	consumerGroup := getenv("WORK_RELEASED_CONSUMER_GROUP", "fulfillment-execution")
	consumer := inboundkafka.NewConsumerWithGroup(kafkaBrokers, workReleasedTopic, consumerGroup, createTask, processedEvents, catalogue, logger)
	defer func() { _ = consumer.Close() }()
	if kafkaCatalogue != nil {
		defer func() { _ = kafkaCatalogue.Close() }()
	}

	go func() {
		logger.Info("http server listening", "addr", httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "error", err)
		}
	}()

	go func() {
		logger.Info("kafka consumer starting", "topic", workReleasedTopic, "brokers", kafkaBrokers)
		if err := consumer.Run(consumerCtx); err != nil {
			logger.Error("kafka consumer stopped", "error", err)
		}
	}()

	// The outbox relay (ADR 0020) runs alongside the HTTP server in the
	// same process, draining outbox_events onto both Kafka topics. It is
	// only wired when both Postgres and the kafka publisher are configured.
	relayDone := make(chan struct{})
	relayCtx, stopRelay := context.WithCancel(context.Background())
	defer stopRelay()
	if relay != nil {
		go func() {
			defer close(relayDone)
			logger.Info("outbox relay running", "topics", []string{outboundkafka.Topic, outboundkafka.AnalyticsTopic})
			if err := relay.Run(relayCtx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("outbox relay stopped", "error", err)
			}
		}()
	} else {
		close(relayDone)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	cancelConsumer()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = srv.Shutdown(ctx)
	// Let the relay finish its in-flight pass so an event committed by a
	// request that completed just before shutdown is not stranded until
	// the next pod boots; bounded by the same shutdown deadline.
	stopRelay()
	select {
	case <-relayDone:
	case <-ctx.Done():
		logger.Warn("outbox relay did not stop before the shutdown deadline")
	}
	return err
}

// buildEventPublisher wires the outbound event publisher, returning it,
// the outbox relay to run alongside the HTTP server (nil when there is
// none), and a close function.
//
// The default is the log publisher, so a local dev run with no Kafka is
// still fully functional. With EVENT_PUBLISHER=kafka every domain event
// is fanned to BOTH the integration topic (TaskCompleted only, enriched)
// and the dedicated analytics topic (ADR-0012):
//
//   - with Postgres configured (pool != nil) the use cases publish into
//     the transactional outbox (ADR 0020): both publishers act only as
//     Encoders inside the use case's transaction, and the relay forwards
//     the stored rows to Kafka. The store and the topics can no longer
//     diverge.
//   - with in-memory adapters (pool == nil) events go straight to the
//     broker through MultiPublisher as before — there is no transaction to
//     bind them to.
func buildEventPublisher(pool *pgxpool.Pool, brokers []string, tasks ports.TaskRepo, stations ports.StationRepo, logger *slog.Logger) (ports.EventPublisher, *postgres.OutboxRelay, func()) {
	if getenv("EVENT_PUBLISHER", "log") != "kafka" {
		logger.Info("event publisher configured", "publisher", "log")
		return events.NewLogPublisher(logger), nil, func() {}
	}

	if pool == nil {
		kafkaPublisher := outboundkafka.NewPublisher(brokers, tasks, stations, uuidLike)
		analyticsPub := outboundkafka.NewAnalyticsPublisher(brokers, tasks, uuidLike)
		logger.Info("event publisher configured", "publisher", "kafka", "mode", "direct",
			"topic", outboundkafka.Topic, "analytics_topic", outboundkafka.AnalyticsTopic, "brokers", brokers)
		return events.NewMultiPublisher(kafkaPublisher, analyticsPub), nil, func() {
			_ = kafkaPublisher.Close()
			_ = analyticsPub.Close()
		}
	}

	// Encoders only: no writer is ever opened for them, the relay's
	// topic-less sink is the single Kafka connection this process holds
	// for publishing.
	integration := outboundkafka.NewPublisherWithWriter(nil, tasks, stations, uuidLike)
	analytics := outboundkafka.NewAnalyticsPublisherWithWriter(nil, tasks, uuidLike)
	sink := outboundkafka.NewRelaySink(brokers)
	relay := postgres.NewOutboxRelay(pool, sink, logger,
		postgres.WithInterval(durationEnv("OUTBOX_RELAY_INTERVAL", time.Second)))
	logger.Info("event publisher configured", "publisher", "kafka", "mode", "outbox",
		"topic", outboundkafka.Topic, "analytics_topic", outboundkafka.AnalyticsTopic, "brokers", brokers)
	return postgres.NewOutboxPublisher(pool, integration, analytics), relay, func() { _ = sink.Close() }
}

// durationEnv parses key as a time.Duration, falling back on absence or a
// malformed/non-positive value: the relay interval is a tuning knob, not
// a contract, so it never fails the boot.
func durationEnv(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
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

// buildAuth wires the fleet-standard REST identity (ADR-0021): static bearer
// keys from API_READ_KEY / API_READWRITE_KEY (MCP_* fallback) and AUTH_MODE
// (enforce|log|off). The default is "enforce" when at least one key is
// configured and "off" — with a loud WARN — when none is, so local runs and
// tests without keys behave exactly as before. Key material is never logged.
func buildAuth(logger *slog.Logger) (*auth.StaticKeyAuth, auth.Mode) {
	authn := auth.NewStaticKeyAuth(auth.KeysFromEnv(os.Getenv))
	defaultMode := auth.ModeOff
	if authn.HasKeys() {
		defaultMode = auth.ModeEnforce
	}
	mode := auth.ParseMode(os.Getenv("AUTH_MODE"), defaultMode)
	if mode == auth.ModeOff {
		logger.Warn("REST auth is OFF: no API_READ_KEY/API_READWRITE_KEY configured or AUTH_MODE=off")
	}
	logger.Info("REST auth configured", "mode", string(mode), "keys", len(auth.KeysFromEnv(os.Getenv)))
	return authn, mode
}

// buildClassificationLookup selects the outbound
// ports.ProductClassificationLookup adapter via PRODUCT_CLASSIFICATION_MODE
// (http|permissive), defaulting to "permissive" so existing tests, CI and
// deployments that do not set the env var are unaffected — mirrors
// inventory-storage's own LOCATION_LOOKUP_MODE=http|permissive pattern for
// its facilitylayout adapter (see ADR-0010). "http" requires
// INVENTORY_STORAGE_BASE_URL; INVENTORY_STORAGE_API_KEY (optional) is sent
// as a bearer token once inventory-storage enforces REST auth (ADR-0021).
func buildClassificationLookup(mode, inventoryStorageBaseURL, apiKey string, logger *slog.Logger) ports.ProductClassificationLookup {
	if !strings.EqualFold(mode, "http") {
		return productclassification.NewPermissiveLookup()
	}
	logger.Info("product classification lookup configured", "mode", "http", "inventory_storage_base_url", inventoryStorageBaseURL, "bearer", apiKey != "")
	return productclassification.NewClient(inventoryStorageBaseURL, nil, productclassification.WithBearerToken(apiKey))
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
