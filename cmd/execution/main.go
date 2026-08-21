// Command execution is the composition root for the Fulfillment Execution
// service: it wires env config to adapters, adapters to use cases, and use
// cases to the HTTP router.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/fulfillment-execution/internal/adapters/inbound/http"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/events"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/postgres"
	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	httpAddr := getenv("HTTP_ADDR", ":8080")
	databaseURL := os.Getenv("DATABASE_URL")

	var (
		taskRepo    ports.TaskRepo
		stationRepo ports.StationRepo
		packageRepo ports.PackageRepo
	)

	if databaseURL == "" {
		log.Println("DATABASE_URL not set, using in-memory adapters")
		taskRepo = memory.NewTaskRepo()
		stationRepo = memory.NewStationRepo()
		packageRepo = memory.NewPackageRepo()
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
	}

	publisher := events.NewLogPublisher(nil)
	clock := memory.SystemClock{}

	handlers := &inboundhttp.Handlers{
		CreateTask:    &usecases.CreateTask{Tasks: taskRepo, Publisher: publisher, Clock: clock, NewId: newTaskId},
		ClaimNext:     &usecases.ClaimNext{Tasks: taskRepo, Stations: stationRepo, Publisher: publisher, Clock: clock},
		RenewLease:    &usecases.RenewLease{Tasks: taskRepo, Clock: clock},
		CompleteTask:  &usecases.CompleteTask{Tasks: taskRepo, Publisher: publisher, Clock: clock},
		SealPackage:   &usecases.SealPackage{Tasks: taskRepo, Packages: packageRepo, Publisher: publisher, Clock: clock, NewId: newPackageId},
		RunSlam:       &usecases.RunSlam{Packages: packageRepo, Publisher: publisher, Clock: clock},
		GetQueueDepth: &usecases.GetQueueDepth{Tasks: taskRepo},
		ExpireLeases:  &usecases.ExpireLeases{Tasks: taskRepo, Publisher: publisher, Clock: clock},
	}
	router := inboundhttp.NewRouter(handlers)

	srv := &http.Server{Addr: httpAddr, Handler: router}

	go func() {
		log.Printf("fulfillment-execution listening on %s", httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
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
