// Command mcp-spike is a THROWAWAY Phase-1 spike (see
// .hermes/plans/2026-08-24_004500-mcp-ai-ecosystem-integration.md and
// docs/docs/adr/0008-mcp-inbound-adapter.md). Its only job is to prove the
// official MCP Go SDK wires cleanly to a real application-layer use case over
// Streamable HTTP. It is NOT the production adapter — that lands in Phase 2 at
// internal/adapters/inbound/mcp/. Delete this package once the spike is
// verified.
//
// Run:    go run ./cmd/mcp-spike       (serves MCP over Streamable HTTP on :8091)
// Verify: npx @modelcontextprotocol/inspector, connect to http://localhost:8091,
//
//	then list tools and call get_queue_status with {"processPath":"PICK"}.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/events"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

// queueStatusInput is the typed tool argument. The `jsonschema` tag becomes the
// property description in the input schema the SDK advertises to clients.
type queueStatusInput struct {
	ProcessPath string `json:"processPath" jsonschema:"the process path queue to inspect: PICK, PACK or SLAM"`
}

// queueStatusOutput is the typed, structured tool result.
type queueStatusOutput struct {
	ProcessPath string `json:"processPath"`
	Depth       int    `json:"depth"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()

	// Real outbound adapter + real use cases — the whole point of the spike is
	// that the MCP tool calls the SAME GetQueueDepth use case the HTTP adapter
	// uses, with nothing forked.
	tasks := memory.NewTaskRepo()
	publisher := events.NewBufferedPublisher()
	clock := memory.SystemClock{}

	// Seed a few Pending tasks so the tool returns real, non-zero data:
	// 2x PICK, 1x PACK.
	create := &usecases.CreateTask{Tasks: tasks, Publisher: publisher, Clock: clock, NewId: newTaskId}
	seed(ctx, create, task.Pick, "order-1", "pick")
	seed(ctx, create, task.Pick, "order-2", "pick")
	seed(ctx, create, task.Pack, "order-3", "pack")

	getQueueDepth := &usecases.GetQueueDepth{Tasks: tasks}

	// Build the MCP server and register one intent-level, read-only tool.
	server := mcp.NewServer(
		&mcp.Implementation{Name: "fulfillment-execution-mcp-spike", Version: "0.0.1"},
		nil,
	)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_queue_status",
		Description: "Return the number of Pending tasks in a process-path queue (PICK, PACK, or SLAM).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in queueStatusInput) (*mcp.CallToolResult, queueStatusOutput, error) {
		depth, err := getQueueDepth.Execute(ctx, task.Type(in.ProcessPath))
		if err != nil {
			return nil, queueStatusOutput{}, err
		}
		return nil, queueStatusOutput{ProcessPath: in.ProcessPath, Depth: depth}, nil
	})

	// Serve over Streamable HTTP (the platform's only supported MCP transport).
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	addr := ":8091"
	if v := os.Getenv("MCP_ADDR"); v != "" {
		addr = v
	}
	log.Printf("mcp-spike serving Streamable HTTP on %s (tool: get_queue_status)", addr)
	return http.ListenAndServe(addr, handler)
}

func seed(ctx context.Context, create *usecases.CreateTask, t task.Type, orderRef string, cap shared.Capability) {
	if _, err := create.Execute(ctx, t, shared.NewCPT(time.Now().Add(time.Hour)), shared.OrderRef(orderRef), shared.NewCapabilitySet(cap)); err != nil {
		log.Fatalf("seed %s: %v", t, err)
	}
}

// newTaskId mirrors the composition root's id strategy (time-ordered, no
// external UUID dependency) — good enough for a spike.
func newTaskId() shared.TaskId {
	return shared.TaskId(time.Now().UTC().Format("20060102T150405.000000000"))
}
