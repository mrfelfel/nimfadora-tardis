package mcpgate

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nimfadora/tardis/internal/config"
)

type Gateway struct {
	cfg       config.GatewayConfig
	mu        sync.RWMutex
	tools     map[string]Tool
	handlers  map[string]ToolHandler
	approvals map[string]*ApprovalRequest
	auditLogs []*AuditLog
}

func New(cfg config.GatewayConfig) *Gateway {
	return &Gateway{
		cfg:       cfg,
		tools:     make(map[string]Tool),
		handlers:  make(map[string]ToolHandler),
		approvals: make(map[string]*ApprovalRequest),
		auditLogs: make([]*AuditLog, 0),
	}
}

// RegisterTool adds a tool with its handler and MCP schema
func (g *Gateway) RegisterTool(tool Tool, handler ToolHandler) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tools[tool.Name] = tool
	g.handlers[tool.Name] = handler
	log.Printf("[mcpgate] registered tool: %s", tool.Name)
}

// ListTools returns tools filtered according to gateway policy
func (g *Gateway) ListTools() []Tool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make([]Tool, 0, len(g.tools))
	for name, tool := range g.tools {
		if g.isToolAllowed(name) {
			result = append(result, tool)
		}
	}
	return result
}

func (g *Gateway) isToolAllowed(name string) bool {
	// Check explicitly denied
	for _, denied := range g.cfg.DeniedTools {
		if denied == name || denied == "*" {
			return false
		}
	}
	// If allowed_tools specified, tool must be in it
	if len(g.cfg.AllowedTools) > 0 {
		found := false
		for _, allowed := range g.cfg.AllowedTools {
			if allowed == name || allowed == "*" {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (g *Gateway) requiresApproval(name string) bool {
	if g.cfg.AllowDangerous {
		return false
	}
	for _, req := range g.cfg.RequireApproval {
		if req == name || req == "*" {
			return true
		}
	}
	return false
}

// CallTool executes a tool through governance, policy, approval checks and audit logging
func (g *Gateway) CallTool(ctx context.Context, agentID, toolName string, args map[string]any) (*ToolResult, error) {
	start := time.Now()

	if !g.isToolAllowed(toolName) {
		err := fmt.Errorf("tool '%s' is forbidden by gateway policy", toolName)
		g.recordAudit(agentID, toolName, args, false, "", time.Since(start).Milliseconds(), true, err.Error())
		return nil, err
	}

	// Human Approval Check
	if g.requiresApproval(toolName) {
		reqID := uuid.New().String()
		req := &ApprovalRequest{
			ID:        reqID,
			AgentID:   agentID,
			ToolName:  toolName,
			Arguments: args,
			Status:    "pending",
			CreatedAt: time.Now(),
		}

		g.mu.Lock()
		g.approvals[reqID] = req
		g.mu.Unlock()

		log.Printf("[mcpgate] APPROVAL REQUIRED for %s by %s (ID: %s)", toolName, agentID, reqID)
		return &ToolResult{
			Content: []ToolContent{
				{
					Type: "text",
					Text: fmt.Sprintf("REQUIRES_HUMAN_APPROVAL: Action '%s' paused for approval. ID: %s. Visit /approval to review.", toolName, reqID),
				},
			},
			IsError: true,
		}, nil
	}

	g.mu.RLock()
	handler, ok := g.handlers[toolName]
	g.mu.RUnlock()

	if !ok {
		err := fmt.Errorf("tool '%s' not found", toolName)
		g.recordAudit(agentID, toolName, args, false, "", time.Since(start).Milliseconds(), true, err.Error())
		return nil, err
	}

	res, err := handler(ctx, args)
	duration := time.Since(start).Milliseconds()

	outputSample := ""
	if res != nil && len(res.Content) > 0 {
		outputSample = res.Content[0].Text
		if len(outputSample) > 200 {
			outputSample = outputSample[:200] + "..."
		}
	}

	isErr := err != nil || (res != nil && res.IsError)
	g.recordAudit(agentID, toolName, args, true, "auto-policy", duration, isErr, outputSample)
	return res, err
}

func (g *Gateway) recordAudit(agentID, toolName string, args map[string]any, approved bool, approvedBy string, duration int64, isError bool, outputSample string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	entry := &AuditLog{
		ID:           uuid.New().String(),
		AgentID:      agentID,
		ToolName:     toolName,
		Arguments:    args,
		Approved:     approved,
		ApprovedBy:   approvedBy,
		DurationMs:   duration,
		Timestamp:    time.Now(),
		IsError:      isError,
		OutputSample: outputSample,
	}

	g.auditLogs = append(g.auditLogs, entry)
	if len(g.auditLogs) > 1000 {
		g.auditLogs = g.auditLogs[len(g.auditLogs)-1000:]
	}
}

func (g *Gateway) GetAuditLogs() []*AuditLog {
	g.mu.RLock()
	defer g.mu.RUnlock()
	copied := make([]*AuditLog, len(g.auditLogs))
	copy(copied, g.auditLogs)
	return copied
}

func (g *Gateway) GetApprovals() []*ApprovalRequest {
	g.mu.RLock()
	defer g.mu.RUnlock()
	res := make([]*ApprovalRequest, 0, len(g.approvals))
	for _, a := range g.approvals {
		res = append(res, a)
	}
	return res
}

func (g *Gateway) ResolveApproval(id string, approve bool, user string) (*ToolResult, error) {
	g.mu.Lock()
	req, ok := g.approvals[id]
	if !ok {
		g.mu.Unlock()
		return nil, fmt.Errorf("approval request %s not found", id)
	}
	now := time.Now()
	req.ResolvedAt = &now
	if approve {
		req.Status = "approved"
	} else {
		req.Status = "rejected"
	}
	toolName := req.ToolName
	args := req.Arguments
	agentID := req.AgentID
	handler, handlerOk := g.handlers[toolName]
	g.mu.Unlock()

	if !approve {
		g.recordAudit(agentID, toolName, args, false, user, 0, true, "User rejected approval")
		return &ToolResult{
			Content: []ToolContent{{Type: "text", Text: "Operation rejected by user."}},
			IsError: true,
		}, nil
	}

	if !handlerOk {
		return nil, fmt.Errorf("tool %s handler not found", toolName)
	}

	start := time.Now()
	res, err := handler(context.Background(), args)
	dur := time.Since(start).Milliseconds()

	outputSample := ""
	if res != nil && len(res.Content) > 0 {
		outputSample = res.Content[0].Text
		if len(outputSample) > 200 {
			outputSample = outputSample[:200] + "..."
		}
	}
	g.recordAudit(agentID, toolName, args, true, user, dur, err != nil, outputSample)
	return res, err
}

// HandleJSONRPC processes incoming MCP JSON-RPC messages
func (g *Gateway) HandleJSONRPC(ctx context.Context, agentID string, raw []byte) (*JSONRPCResponse, error) {
	var req JSONRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   &JSONRPCError{Code: -32700, Message: "Parse error"},
		}, nil
	}

	switch req.Method {
	case "initialize":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2026-07-28",
				"capabilities": map[string]any{
					"tools": map[string]any{
						"listChanged": false,
					},
				},
				"serverInfo": map[string]any{
					"name":    "TARDIS-MCPGate",
					"version": "1.0.0",
				},
			},
		}, nil

	case "tools/list":
		tools := g.ListTools()
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": tools,
			},
		}, nil

	case "tools/call":
		var params CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &JSONRPCError{Code: -32602, Message: "Invalid params"},
			}, nil
		}

		res, err := g.CallTool(ctx, agentID, params.Name, params.Arguments)
		if err != nil {
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &JSONRPCError{Code: -32000, Message: err.Error()},
			}, nil
		}
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  res,
		}, nil

	default:
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32601, Message: fmt.Sprintf("Method %s not found", req.Method)},
		}, nil
	}
}
