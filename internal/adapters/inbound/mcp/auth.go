package mcp

import (
	"github.com/claudioed/fulfillment-execution/internal/adapters/inbound/auth"
)

// The MCP adapter's identity model is the fleet-standard one implemented
// once per repository in internal/adapters/inbound/auth (ADR-0021, adopting
// warehouse-ops-agent ADR 0005). The Scope / Authenticator / StaticKeyAuth
// types that used to live here are now aliases to that package so both the
// REST and the MCP surfaces share exactly one implementation — the seam
// described in ADR-0008 (swap StaticKeyAuth for an OAuth 2.1 resource-server
// Authenticator without touching any tool handler) is unchanged.

// Scope is a coarse authorization class carried by an API key.
type Scope = auth.Scope

const (
	ScopeRead      = auth.ScopeRead
	ScopeReadWrite = auth.ScopeReadWrite
)

// Authenticator validates a request's bearer credential and reports the scope
// it grants.
type Authenticator = auth.Authenticator

// StaticKeyAuth authenticates a request against a fixed set of bearer API
// keys, each mapped to a scope.
type StaticKeyAuth = auth.StaticKeyAuth

// NewStaticKeyAuth builds a StaticKeyAuth from token->scope pairs. Empty tokens
// are ignored so a blank env var cannot silently authorize every request.
func NewStaticKeyAuth(keys map[string]Scope) *StaticKeyAuth {
	return auth.NewStaticKeyAuth(keys)
}

// scopeAllows reports whether a granted scope may call a tool requiring the
// given minimum scope. read-write satisfies everything; read satisfies only
// read.
func scopeAllows(granted, required Scope) bool {
	return auth.Allows(granted, required)
}
