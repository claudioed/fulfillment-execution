package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

// tracerName is the OTel instrumentation scope for MCP tool spans.
const tracerName = "github.com/claudioed/fulfillment-execution/internal/adapters/inbound/mcp"

// Deps is everything the MCP tools need, injected by the composition root.
// It carries the same use cases the HTTP adapter uses plus the narrow read
// port; the adapter never constructs an outbound adapter itself.
type Deps struct {
	// GetQueueDepth is the existing read-model use case, reused unchanged.
	GetQueueDepth *usecases.GetQueueDepth
	// Tasks is the read-only query port for richer diagnostics.
	Tasks TaskQueries
	// Now supplies the current time; injected so lease diagnostics are
	// deterministic under test. Defaults to time.Now when nil.
	Now func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// --- get_queue_status ---------------------------------------------------------

type queueStatusInput struct {
	ProcessPath string `json:"processPath" jsonschema:"the process path queue to inspect: PICK, PACK or SLAM"`
}

type queueStatusOutput struct {
	ProcessPath string `json:"processPath"`
	Depth       int    `json:"depth"`
}

func (d Deps) getQueueStatus(ctx context.Context, in queueStatusInput) (queueStatusOutput, error) {
	t, err := parseProcessPath(in.ProcessPath)
	if err != nil {
		return queueStatusOutput{}, err
	}
	depth, err := d.GetQueueDepth.Execute(ctx, t)
	if err != nil {
		return queueStatusOutput{}, err
	}
	return queueStatusOutput{ProcessPath: string(t), Depth: depth}, nil
}

// --- find_claimable_work ------------------------------------------------------

type findClaimableInput struct {
	ProcessPath string `json:"processPath" jsonschema:"the process path to search: PICK, PACK or SLAM"`
}

type findClaimableOutput struct {
	ProcessPath    string `json:"processPath"`
	CandidateCount int    `json:"candidateCount"`
	// Best is the highest-priority (earliest CPT) claimable task, or null when
	// the queue has nothing claimable right now.
	Best *claimableWork `json:"best"`
}

func (d Deps) findClaimableWork(ctx context.Context, in findClaimableInput) (findClaimableOutput, error) {
	t, err := parseProcessPath(in.ProcessPath)
	if err != nil {
		return findClaimableOutput{}, err
	}
	candidates, err := d.Tasks.FindClaimableByType(ctx, t, d.now())
	if err != nil {
		return findClaimableOutput{}, err
	}
	out := findClaimableOutput{ProcessPath: string(t), CandidateCount: len(candidates)}
	if len(candidates) > 0 {
		best := toClaimableWork(candidates[0])
		out.Best = &best
	}
	return out, nil
}

// --- diagnose_stuck_tasks -----------------------------------------------------

type diagnoseInput struct {
	// WithinSeconds flags claimed tasks whose lease expires within this many
	// seconds (as well as any already expired). 0 means "only already expired".
	WithinSeconds int `json:"withinSeconds" jsonschema:"flag claimed tasks whose lease expires within this many seconds; 0 = only already-expired leases"`
}

type diagnoseOutput struct {
	Count int         `json:"count"`
	Tasks []stuckTask `json:"tasks"`
}

func (d Deps) diagnoseStuckTasks(ctx context.Context, in diagnoseInput) (diagnoseOutput, error) {
	now := d.now()
	claimed, err := d.Tasks.FindAllClaimed(ctx)
	if err != nil {
		return diagnoseOutput{}, err
	}
	horizon := now.Add(time.Duration(in.WithinSeconds) * time.Second)
	flagged := make([]stuckTask, 0)
	for _, t := range claimed {
		lease := t.Lease()
		if lease == nil {
			continue
		}
		reason := ""
		switch {
		case !lease.Expiry.After(now):
			reason = "lease already expired; task will return to the pool on the next sweep"
		case !lease.Expiry.After(horizon):
			reason = "lease expiring within the requested window"
		default:
			continue
		}
		flagged = append(flagged, stuckTask{
			TaskId:         string(t.Id()),
			Type:           string(t.Type()),
			LeaseStationId: string(lease.StationId),
			LeaseExpiry:    lease.Expiry.UTC().Format(time.RFC3339),
			Reason:         reason,
		})
	}
	return diagnoseOutput{Count: len(flagged), Tasks: flagged}, nil
}

// --- registration -------------------------------------------------------------

// registerTools adds every read tool to the server, each wrapped so its
// handler runs inside an OTel span named "mcp.tool <name>" and is gated by the
// session's scope. All three tools require only ScopeRead.
func (d Deps) registerTools(server *mcp.Server, scopeOf func(context.Context) Scope) {
	readOnly := true

	addRead(server, scopeOf, &mcp.Tool{
		Name:        "get_queue_status",
		Description: "Return the number of Pending tasks in a process-path queue (PICK, PACK, or SLAM).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.getQueueStatus)

	addRead(server, scopeOf, &mcp.Tool{
		Name:        "find_claimable_work",
		Description: "Return the highest-priority (earliest-CPT) task a station could claim now in a given process path, plus how many candidates exist.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.findClaimableWork)

	addRead(server, scopeOf, &mcp.Tool{
		Name:        "diagnose_stuck_tasks",
		Description: "List claimed tasks whose lease has expired (or expires within a given window), each with the reason it is flagged.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.diagnoseStuckTasks)
}

// addRead registers one read-scoped tool. It centralises the cross-cutting
// concerns every tool shares: a span per call, scope enforcement, and mapping
// a handler error onto the span before returning it.
func addRead[In, Out any](
	server *mcp.Server,
	scopeOf func(context.Context) Scope,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		ctx, span := otel.Tracer(tracerName).Start(ctx, "mcp.tool "+tool.Name,
			trace.WithAttributes(attribute.String("mcp.tool.name", tool.Name)),
		)
		defer span.End()

		if !scopeAllows(scopeOf(ctx), ScopeRead) {
			err := fmt.Errorf("tool %q requires read scope", tool.Name)
			span.SetStatus(codes.Error, "unauthorized")
			return nil, zero, err
		}

		out, err := handle(ctx, in)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, zero, err
		}
		return nil, out, nil
	})
}

// parseProcessPath validates a client-supplied process-path string against the
// domain's known task types. Tool arguments come from a model and are
// untrusted, so an unknown value is rejected rather than silently defaulted.
func parseProcessPath(s string) (task.Type, error) {
	switch task.Type(s) {
	case task.Pick, task.Pack, task.Slam:
		return task.Type(s), nil
	default:
		return "", fmt.Errorf("unknown process path %q: must be PICK, PACK or SLAM", s)
	}
}
