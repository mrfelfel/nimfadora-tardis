package mcpgate

import (
	"context"
	"encoding/json"
	"time"
)

// MCP Protocol 2026-07-28 & JSON-RPC types

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Tool Definition following MCP standard
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type CallToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"` // "text", "image", "resource"
	Text string `json:"text,omitempty"`
}

type ToolHandler func(ctx context.Context, args map[string]any) (*ToolResult, error)

// AuditLog records every single tool call for compliance and control plane observability
type AuditLog struct {
	ID           string         `json:"id"`
	AgentID      string         `json:"agent_id"`
	ToolName     string         `json:"tool_name"`
	Arguments    map[string]any `json:"arguments"`
	Approved     bool           `json:"approved"`
	ApprovedBy   string         `json:"approved_by,omitempty"`
	DurationMs   int64          `json:"duration_ms"`
	Timestamp    time.Time      `json:"timestamp"`
	IsError      bool           `json:"is_error"`
	OutputSample string         `json:"output_sample,omitempty"`
}

// ApprovalRequest represents a pending action requiring human confirmation
type ApprovalRequest struct {
	ID        string         `json:"id"`
	AgentID   string         `json:"agent_id"`
	ToolName  string         `json:"tool_name"`
	Arguments map[string]any `json:"arguments"`
	Status    string         `json:"status"` // "pending", "approved", "rejected"
	CreatedAt time.Time      `json:"created_at"`
	ResolvedAt *time.Time    `json:"resolved_at,omitempty"`
}
