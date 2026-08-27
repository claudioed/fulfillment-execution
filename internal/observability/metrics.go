package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

// taskTypeKey is the attribute carrying Pick | Pack | SLAM on the task
// counters, so queue throughput can be broken down by process path.
const taskTypeKey = attribute.Key("task.type")

// Metrics is the outbound adapter satisfying ports.Metrics: it records the
// business events of the task lifecycle -- tasks claimed by a station and
// tasks completed -- as OTel counters exported through the global
// MeterProvider.
type Metrics struct {
	claimed   metric.Int64Counter
	completed metric.Int64Counter
}

var _ ports.Metrics = (*Metrics)(nil)

// NewMetrics creates the task lifecycle counters on the global
// MeterProvider. Setup must have run first for them to reach a Collector;
// before that they resolve against the no-op provider and simply record
// nothing.
func NewMetrics() (*Metrics, error) {
	meter := otel.Meter(InstrumentationName)

	claimed, err := meter.Int64Counter(
		"fulfillment.tasks.claimed",
		metric.WithDescription("Tasks leased to a station by claimNext, by task type."),
		metric.WithUnit("{task}"),
	)
	if err != nil {
		return nil, err
	}
	completed, err := meter.Int64Counter(
		"fulfillment.tasks.completed",
		metric.WithDescription("Tasks completed by the claiming station, by task type."),
		metric.WithUnit("{task}"),
	)
	if err != nil {
		return nil, err
	}
	return &Metrics{claimed: claimed, completed: completed}, nil
}

func (m *Metrics) TaskClaimed(ctx context.Context, taskType task.Type) {
	m.claimed.Add(ctx, 1, metric.WithAttributes(taskTypeKey.String(string(taskType))))
}

func (m *Metrics) TaskCompleted(ctx context.Context, taskType task.Type) {
	m.completed.Add(ctx, 1, metric.WithAttributes(taskTypeKey.String(string(taskType))))
}
