package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer builds the MCP server for this bounded context with every read
// tool, scoped resource, and workflow prompt registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "fulfillment-execution-mcp", Version: "1.0.0"},
		&mcp.ServerOptions{
			Instructions: "Read-only access to the fulfillment-execution work backlog: queue depth by process path (PICK/PACK/SLAM), the most urgent claimable task, and lapsing leases. Start with the triage_backlog prompt.",
		},
	)

	deps.registerTools(server)
	deps.registerResources(server)
	deps.registerPrompts(server)

	return server
}

// Handler returns the Streamable HTTP handler for the MCP server. The server
// is mounted unauthenticated: this bounded context's REST and MCP surfaces
// carry no identity layer.
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}
