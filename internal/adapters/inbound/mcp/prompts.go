package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// triageBacklogSOP is the operational standard-operating-procedure the
// triage_backlog prompt hands to the model. Per the charter, prompts encode
// how to interpret results, when to escalate, and what "done" means — they
// standardise agent behaviour across clients rather than leaving procedure
// implicit.
const triageBacklogSOP = `You are triaging the fulfillment-execution work backlog. Use only the MCP tools; never assume state.

Procedure:
1. Call get_queue_status for PICK, PACK, and SLAM. The deepest queue is the most backed up.
2. For the deepest queue, call find_claimable_work to see the highest-priority (earliest-CPT) task waiting and how many candidates exist. An earlier CPT is more urgent.
3. Call diagnose_stuck_tasks (start with withinSeconds=0 for already-expired leases, then a small window such as 60) to find claimed work whose lease is lapsing — these will fall back to the pool and add churn.

Interpretation:
- A deep queue with many claimable candidates is a throughput problem (not enough stations pulling).
- Stuck/expiring leases are a reliability problem (claims not being completed or renewed).

Escalate to a human when: any queue depth is growing across repeated checks, or the same tasks keep appearing in diagnose_stuck_tasks (a task that cannot be completed).

Done means: you have reported, per process path, the queue depth, the most urgent claimable task, and any expired/expiring leases — with a one-line reason for each concern. Do not attempt to change state; this backlog triage is read-only.`

// registerPrompts adds the workflow prompts (operational SOPs).
func (d Deps) registerPrompts(server *mcp.Server, scopeOf func(context.Context) Scope) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "triage_backlog",
		Description: "Standard operating procedure for triaging the fulfillment-execution work backlog using the read tools.",
	}, func(ctx context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Description: "How to triage the work backlog: read queues, find urgent work, surface lapsing leases, and know when to escalate.",
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: triageBacklogSOP},
			}},
		}, nil
	})
}
