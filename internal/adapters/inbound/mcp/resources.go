package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

// registerResources adds the read-model resources. Per the charter,
// resources are bounded context contracts tied to a decision, not bulk dumps:
// each queue-status resource answers "how deep is this one process path?".
func (d Deps) registerResources(server *mcp.Server) {
	for _, t := range []task.Type{task.Pick, task.Pack, task.Slam} {
		t := t
		uri := fmt.Sprintf("queue://fulfillment/%s/status", t)
		server.AddResource(&mcp.Resource{
			URI:         uri,
			Name:        fmt.Sprintf("%s queue status", t),
			Description: fmt.Sprintf("Pending-task depth for the %s process path.", t),
			MIMEType:    "application/json",
		}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			depth, err := d.GetQueueDepth.Execute(ctx, t)
			if err != nil {
				return nil, err
			}
			body, err := json.Marshal(queueStatusOutput{ProcessPath: string(t), Depth: depth})
			if err != nil {
				return nil, err
			}
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      uri,
					MIMEType: "application/json",
					Text:     string(body),
				}},
			}, nil
		})
	}
}
