// Command execution is the composition root for the Fulfillment Execution
// service: it wires env config to adapters, adapters to use cases, and use
// cases to the HTTP router.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/fulfillment-execution/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/fulfillment-execution/internal/adapters/inbound/kafka"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/events"
	outboundkafka "github.com/claudioed/fulfillment-execution/internal/adapters/outbound/kafka"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/postgres"
	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
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

	httpAddr := getenv("HTTP_ADDR", ":8080")
	databaseURL := os.Getenv("DATABASE_URL")
	kafkaBrokers := strings.Split(getenv("KAFKA_BROKERS", "localhost:9092"), ",")

	var (
		taskRepo        ports.TaskRepo
		stationRepo     ports.StationRepo
		packageRepo     ports.PackageRepo
		processedEvents ports.ProcessedEvents
	)

	if databaseURL == "" {
		logger.Info("database url not configured; using in-memory adapters")
		taskRepo = memory.NewTaskRepo()
		stationRepo = memory.NewStationRepo()
		packageRepo = memory.NewPackageRepo()
		processedEvents = memory.NewProcessedEventsRepo()
	} else {
		if err := postgres.Migrate(databaseURL, "migrations"); err != nil {
			return err
		}
		pool, err := pgxpool.New(context.Background(), databaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		taskRepo = postgres.NewTaskRepo(pool)
		stationRepo = postgres.NewStationRepo(pool)
		packageRepo = postgres.NewPackageRepo(pool)
		processedEvents = postgres.NewProcessedEventsRepo(pool)
	}

	var (
		publisher      ports.EventPublisher
		kafkaPublisher *outboundkafka.Publisher
	)
	if getenv("EVENT_PUBLISHER", "log") == "kafka" {
		logger.Info("event publisher configured", "publisher", "kafka", "topic", outboundkafka.Topic, "brokers", kafkaBrokers)
		kafkaPublisher = outboundkafka.NewPublisher(kafkaBrokers, taskRepo, uuidLike)
		publisher = kafkaPublisher
	} else {
		publisher = events.NewLogPublisher(logger)
	}
	clock := memory.SystemClock{}

	createTask := &usecases.CreateTask{Tasks: taskRepo, Publisher: publisher, Clock: clock, NewId: newTaskId}

	handlers := &inboundhttp.Handlers{
		CreateTask:      createTask,
		ClaimNext:       &usecases.ClaimNext{Tasks: taskRepo, Stations: stationRepo, Publisher: publisher, Clock: clock},
		RenewLease:      &usecases.RenewLease{Tasks: taskRepo, Clock: clock},
		CompleteTask:    &usecases.CompleteTask{Tasks: taskRepo, Publisher: publisher, Clock: clock},
		SealPackage:     &usecases.SealPackage{Tasks: taskRepo, Packages: packageRepo, Publisher: publisher, Clock: clock, NewId: newPackageId},
		RunSlam:         &usecases.RunSlam{Packages: packageRepo, Publisher: publisher, Clock: clock},
		GetQueueDepth:   &usecases.GetQueueDepth{Tasks: taskRepo},
		ExpireLeases:    &usecases.ExpireLeases{Tasks: taskRepo, Publisher: publisher, Clock: clock},
		RegisterStation: &usecases.RegisterStation{Stations: stationRepo, Publisher: publisher},
	}
	router := inboundhttp.NewRouter(handlers, logger)

	srv := &http.Server{Addr: httpAddr, Handler: router}

	consumer := inboundkafka.NewConsumer(kafkaBrokers, workReleasedTopic, createTask, processedEvents, logger)
	defer func() { _ = consumer.Close() }()
	if kafkaPublisher != nil {
		defer func() { _ = kafkaPublisher.Close() }()
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

// newLogger builds a JSON slog.Logger writing to stdout, with its minimum
// level set from a LOG_LEVEL value (debug|info|warn|warning|error,
// case-insensitive, defaulting to info for anything else).
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
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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
