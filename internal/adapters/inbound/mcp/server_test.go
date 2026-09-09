package mcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/fulfillment-execution/internal/adapters/inbound/mcp"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/events"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

// newServer builds a real MCP HTTP server over an in-memory repo seeded with
// 2x PICK + 1x PACK pending tasks, returns its httptest URL.
func newServer(t *testing.T) string {
	t.Helper()
	tasks := memory.NewTaskRepo()
	publisher := events.NewBufferedPublisher()
	clock := memory.NewFixedClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	n := 0
	create := &usecases.CreateTask{Tasks: tasks, Publisher: publisher, Clock: clock, NewId: func() shared.TaskId {
		n++
		return shared.TaskId([]byte{'t', byte('0' + n)})
	}}
	ctx := context.Background()
	_, _ = create.Execute(ctx, task.Pick, shared.NewCPT(clock.Now().Add(time.Hour)), "o1", shared.NewCapabilitySet("pick"), false, false)
	_, _ = create.Execute(ctx, task.Pick, shared.NewCPT(clock.Now().Add(2*time.Hour)), "o2", shared.NewCapabilitySet("pick"), false, false)
	_, _ = create.Execute(ctx, task.Pack, shared.NewCPT(clock.Now().Add(time.Hour)), "o3", shared.NewCapabilitySet("pack"), false, false)

	deps := inboundmcp.Deps{
		GetQueueDepth: &usecases.GetQueueDepth{Tasks: tasks},
		Tasks:         tasks,
		Now:           clock.Now,
	}
	server := inboundmcp.NewServer(deps)
	httpSrv := httptest.NewServer(inboundmcp.Handler(server))
	t.Cleanup(httpSrv.Close)
	return httpSrv.URL
}

func connect(t *testing.T, url string) *sdk.ClientSession {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	transport := &sdk.StreamableClientTransport{
		Endpoint:   url,
		HTTPClient: &http.Client{},
	}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestServer_ToolsListAndCall(t *testing.T) {
	url := newServer(t)
	session := connect(t, url)
	ctx := context.Background()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	want := map[string]bool{"get_queue_status": false, "find_claimable_work": false, "diagnose_stuck_tasks": false}
	for _, tool := range tools.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %q not advertised", name)
		}
	}

	res, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name:      "get_queue_status",
		Arguments: map[string]any{"processPath": "PICK"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	depth, ok := res.StructuredContent.(map[string]any)["depth"]
	if !ok {
		t.Fatalf("no depth in structured content: %+v", res.StructuredContent)
	}
	if depth.(float64) != 2 {
		t.Fatalf("PICK depth = %v, want 2", depth)
	}
}

func TestServer_CallToolRejectsUnknownProcessPath(t *testing.T) {
	url := newServer(t)
	session := connect(t, url)
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "get_queue_status",
		Arguments: map[string]any{"processPath": "FLYING"},
	})
	if err != nil {
		t.Fatalf("call tool transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected tool-level error for unknown process path")
	}
}

func TestServer_ResourceRead(t *testing.T) {
	url := newServer(t)
	session := connect(t, url)
	res, err := session.ReadResource(context.Background(), &sdk.ReadResourceParams{
		URI: "queue://fulfillment/PICK/status",
	})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(res.Contents) == 0 || res.Contents[0].Text == "" {
		t.Fatalf("empty resource contents: %+v", res.Contents)
	}
}

func TestServer_PromptGet(t *testing.T) {
	url := newServer(t)
	session := connect(t, url)
	res, err := session.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "triage_backlog"})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(res.Messages) == 0 {
		t.Fatal("triage_backlog prompt returned no messages")
	}
}

// newWriteServer builds a server with a single PICK task already claimed by
// station s1 (ready to complete). Returns the URL and the claimed task's id.
func newWriteServer(t *testing.T) (string, string) {
	t.Helper()
	tasks := memory.NewTaskRepo()
	stations := memory.NewStationRepo()
	publisher := events.NewBufferedPublisher()
	clock := memory.NewFixedClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	n := 0
	create := &usecases.CreateTask{Tasks: tasks, Publisher: publisher, Clock: clock, NewId: func() shared.TaskId {
		n++
		return shared.TaskId([]byte{'t', byte('0' + n)})
	}}
	register := &usecases.RegisterStation{Stations: stations, Publisher: publisher}
	claim := &usecases.ClaimNext{Tasks: tasks, Stations: stations, Publisher: publisher, Clock: clock}
	ctx := context.Background()
	if _, err := register.Execute(ctx, "s1", []string{"pick"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := create.Execute(ctx, task.Pick, shared.NewCPT(clock.Now().Add(time.Hour)), "o1", shared.NewCapabilitySet("pick"), false, false); err != nil {
		t.Fatalf("create: %v", err)
	}
	claimed, err := claim.Execute(ctx, "s1", task.Pick)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	deps := inboundmcp.Deps{
		GetQueueDepth: &usecases.GetQueueDepth{Tasks: tasks},
		CompleteTask:  &usecases.CompleteTask{Tasks: tasks, Publisher: publisher, Clock: clock},
		Tasks:         tasks,
		Now:           clock.Now,
	}
	server := inboundmcp.NewServer(deps)
	httpSrv := httptest.NewServer(inboundmcp.Handler(server))
	t.Cleanup(httpSrv.Close)
	return httpSrv.URL, string(claimed.Id())
}

func TestServer_CompleteTaskSucceeds(t *testing.T) {
	url, taskId := newWriteServer(t)
	session := connect(t, url)
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "complete_task",
		Arguments: map[string]any{"taskId": taskId, "stationId": "s1"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("complete_task returned error: %+v", res.Content)
	}
	completed, ok := res.StructuredContent.(map[string]any)["completed"]
	if !ok || completed != true {
		t.Fatalf("expected completed=true, got %+v", res.StructuredContent)
	}

	// Idempotency / at-most-once over the wire: a second completion is rejected.
	again, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "complete_task",
		Arguments: map[string]any{"taskId": taskId, "stationId": "s1"},
	})
	if err != nil {
		t.Fatalf("second call transport error: %v", err)
	}
	if !again.IsError {
		t.Fatal("second complete_task must be rejected by the at-most-once invariant")
	}
}
