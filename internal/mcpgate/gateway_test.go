package mcpgate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nimfadora/tardis/internal/config"
)

func TestGatewayPolicyAndToolExecution(t *testing.T) {
	cfg := config.GatewayConfig{
		RequireApproval: []string{"dangerous_tool"},
		DeniedTools:     []string{"banned_tool"},
	}
	gw := New(cfg)

	// Register safe tool
	gw.RegisterTool(Tool{
		Name:        "echo_tool",
		Description: "echoes message",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, args map[string]any) (*ToolResult, error) {
		msg, _ := args["msg"].(string)
		return &ToolResult{
			Content: []ToolContent{{Type: "text", Text: "Echo: " + msg}},
		}, nil
	})

	// Register dangerous tool
	gw.RegisterTool(Tool{
		Name:        "dangerous_tool",
		Description: "deletes database or executes dangerous action",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, args map[string]any) (*ToolResult, error) {
		return &ToolResult{
			Content: []ToolContent{{Type: "text", Text: "Danger executed"}},
		}, nil
	})

	// Register banned tool
	gw.RegisterTool(Tool{
		Name:        "banned_tool",
		Description: "banned tool",
	}, func(ctx context.Context, args map[string]any) (*ToolResult, error) {
		return &ToolResult{Content: []ToolContent{{Type: "text", Text: "Banned"}}}, nil
	})

	ctx := context.Background()

	// 1. Call safe tool
	res, err := gw.CallTool(ctx, "test-agent", "echo_tool", map[string]any{"msg": "hello"})
	if err != nil {
		t.Fatalf("unexpected error calling echo_tool: %v", err)
	}
	if len(res.Content) == 0 || res.Content[0].Text != "Echo: hello" {
		t.Fatalf("unexpected response: %+v", res)
	}

	// 2. Call dangerous tool -> should require human approval
	res, err = gw.CallTool(ctx, "test-agent", "dangerous_tool", map[string]any{"target": "all"})
	if err != nil {
		t.Fatalf("call to dangerous_tool should not panic, but require approval: %v", err)
	}
	if !res.IsError || len(res.Content) == 0 {
		t.Fatalf("expected approval notice in response, got: %+v", res)
	}

	approvals := gw.GetApprovals()
	if len(approvals) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(approvals))
	}
	approvalID := approvals[0].ID

	// 3. Approve the request
	resolvedRes, err := gw.ResolveApproval(approvalID, true, "admin")
	if err != nil {
		t.Fatalf("failed to resolve approval: %v", err)
	}
	if resolvedRes.Content[0].Text != "Danger executed" {
		t.Fatalf("unexpected resolved response: %+v", resolvedRes)
	}

	// 4. Call banned tool -> forbidden
	_, err = gw.CallTool(ctx, "test-agent", "banned_tool", nil)
	if err == nil {
		t.Fatalf("expected error calling banned tool, got nil")
	}

	// 5. Verify audit logs
	logs := gw.GetAuditLogs()
	if len(logs) < 3 {
		t.Fatalf("expected at least 3 audit logs, got %d", len(logs))
	}
}

func TestGatewayJSONRPC(t *testing.T) {
	cfg := config.GatewayConfig{}
	gw := New(cfg)

	gw.RegisterTool(Tool{
		Name:        "ping",
		Description: "ping tool",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, args map[string]any) (*ToolResult, error) {
		return &ToolResult{Content: []ToolContent{{Type: "text", Text: "pong"}}}, nil
	})

	ctx := context.Background()

	// Test initialize
	initReq := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	resp, err := gw.HandleJSONRPC(ctx, "agent-1", initReq)
	if err != nil || resp.Error != nil {
		t.Fatalf("failed initialize JSON-RPC: err=%v, rpcErr=%+v", err, resp.Error)
	}

	// Test tools/list
	listReq := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	resp, err = gw.HandleJSONRPC(ctx, "agent-1", listReq)
	if err != nil || resp.Error != nil {
		t.Fatalf("failed tools/list JSON-RPC: err=%v, rpcErr=%+v", err, resp.Error)
	}

	// Test tools/call
	callReq := []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ping"}}`)
	resp, err = gw.HandleJSONRPC(ctx, "agent-1", callReq)
	if err != nil || resp.Error != nil {
		t.Fatalf("failed tools/call JSON-RPC: err=%v, rpcErr=%+v", err, resp.Error)
	}
}
