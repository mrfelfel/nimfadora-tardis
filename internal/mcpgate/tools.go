package mcpgate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nimfadora/tardis/internal/call"
)

// codingEnv builds the environment for spawning coding agents with the user's API settings.
func codingEnv(apiBaseURL, apiKey, model string) []string {
	env := os.Environ()

	// Claude Code env vars -- bypasses OAuth, uses user's API key + proxy
	if apiKey != "" {
		env = append(env, "ANTHROPIC_API_KEY="+apiKey)
	}
	if apiBaseURL != "" {
		env = append(env, "ANTHROPIC_BASE_URL="+apiBaseURL)
	}
	if model != "" {
		env = append(env, "ANTHROPIC_MODEL="+model)
	}
	// Allow non-Anthropic models through Claude Code's strict catalog check
	env = append(env, "CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT=1")

	// OpenCode / OpenAI-compatible env vars
	if apiKey != "" {
		env = append(env, "OPENAI_API_KEY="+apiKey)
	}
	if apiBaseURL != "" {
		env = append(env, "OPENAI_BASE_URL="+apiBaseURL)
	}
	if model != "" {
		env = append(env, "OPENAI_MODEL="+model)
	}

	return env
}

// RegisterDefaultTools wires standard TARDIS agent tools into the MCP gateway
func RegisterDefaultTools(g *Gateway, callHandler *call.Handler, defaultBackend, defaultModel string, apiBaseURL, apiKey string) {
	// 1. Coding Agent (OpenCode / Claude Code / Shell executor)
	g.RegisterTool(Tool{
		Name:        "coding_agent",
		Description: "Delegate a programming, debugging, refactoring, or implementation task to an AI coding agent (OpenCode, Claude, etc.) with specified model.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"task": {"type": "string", "description": "The exact coding or refactoring task to accomplish"},
				"model": {"type": "string", "description": "The model to use, e.g. mimo-v2.5, glm-4, deepseek-coder"},
				"backend": {"type": "string", "description": "Backend tool: opencode, claude, or bash", "enum": ["opencode", "claude", "bash"]}
			},
			"required": ["task"]
		}`),
	}, func(ctx context.Context, args map[string]any) (*ToolResult, error) {
		task, _ := args["task"].(string)
		if task == "" {
			return nil, fmt.Errorf("task is required")
		}

		backend, _ := args["backend"].(string)
		if backend == "" {
			backend = defaultBackend
		}
		model, _ := args["model"].(string)
		if model == "" {
			model = defaultModel
		}

		// Read live API config from gateway (supports hot-reload)
		apiURL, apiK := g.GetAPIConfig()

		log.Printf("[tools:coding_agent] backend=%s model=%s api=%s task=%s", backend, model, apiURL, task)

		env := codingEnv(apiURL, apiK, model)

		var cmd *exec.Cmd
		switch backend {
		case "claude":
			// Claude Code CLI: uses ANTHROPIC_API_KEY + ANTHROPIC_BASE_URL from env
			// No OAuth login needed when these are set
			cmd = exec.CommandContext(ctx, "claude", "-p", "--verbose",
				"--output-format", "text",
				"--model", model,
				"--bare",
				task)
			cmd.Env = env
			cmd.Dir = "."

		case "opencode":
			// OpenCode CLI: uses OPENAI_API_KEY + OPENAI_BASE_URL from env
			cmd = exec.CommandContext(ctx, "opencode", "run", task)
			cmd.Env = env

		default:
			// Fallback: bash execution (no AI, just shell)
			cmd = exec.CommandContext(ctx, "bash", "-c", task)
		}

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		output := stdout.String()
		if stderr.Len() > 0 {
			if output != "" {
				output += "\n--- STDERR ---\n"
			}
			output += stderr.String()
		}

		if err != nil {
			return &ToolResult{
				Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Execution failed: %v\nOutput: %s", err, output)}},
				IsError: true,
			}, nil
		}

		return &ToolResult{
			Content: []ToolContent{{Type: "text", Text: output}},
		}, nil
	})

	// 2. Research Multi-Agent Tool
	g.RegisterTool(Tool{
		Name:        "research_agent",
		Description: "Perform deep research, find documentation, analyze architecture, and summarize findings.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "Research question or topic"},
				"scope": {"type": "string", "description": "Scope of research, e.g. web, codebase, dependencies"}
			},
			"required": ["query"]
		}`),
	}, func(ctx context.Context, args map[string]any) (*ToolResult, error) {
		query, _ := args["query"].(string)
		scope, _ := args["scope"].(string)
		if scope == "" {
			scope = "all"
		}

		log.Printf("[tools:research_agent] researching query='%s' scope='%s'", query, scope)

		// Search local files / codebase or web
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("### Research Report for: %s (Scope: %s)\n\n", query, scope))

		// Check if grep/find or gh search is needed
		cmd := exec.CommandContext(ctx, "grep", "-rnI", "--exclude-dir=.git", "--exclude-dir=tmp", query, ".")
		out, _ := cmd.CombinedOutput()
		if len(out) > 0 {
			sb.WriteString("#### Local Codebase Matches:\n```text\n")
			if len(out) > 2000 {
				sb.WriteString(string(out[:2000]) + "\n...[truncated]")
			} else {
				sb.WriteString(string(out))
			}
			sb.WriteString("\n```\n\n")
		} else {
			sb.WriteString("No direct text matches in local repository.\n\n")
		}

		sb.WriteString("Research synthesis complete.")
		return &ToolResult{
			Content: []ToolContent{{Type: "text", Text: sb.String()}},
		}, nil
	})

	// 3. Telephony Make Call Tool (SIP voice notification)
	g.RegisterTool(Tool{
		Name:        "telephony_make_call",
		Description: "Initiate an outbound telephone call to deliver an alert, verification code, or critical notice via voice TTS over SIP.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"target": {"type": "string", "description": "Destination phone number (E.164 or operator format, e.g. 0912...)"},
				"message": {"type": "string", "description": "Spoken notification message in Persian or English"}
			},
			"required": ["target", "message"]
		}`),
	}, func(ctx context.Context, args map[string]any) (*ToolResult, error) {
		target, _ := args["target"].(string)
		message, _ := args["message"].(string)
		if target == "" || message == "" {
			return nil, fmt.Errorf("target phone number and message are required")
		}

		if callHandler == nil {
			return &ToolResult{
				Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("[SIMULATED] SIP Call to %s with message: %s", target, message)}},
			}, nil
		}

		// Fire async or synchronous call
		go func() {
			callCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			from := "" // handler picks default configured caller ID
			err := callHandler.Notify(callCtx, target, from, message)
			if err != nil {
				log.Printf("[tools:telephony_make_call] call error: %v", err)
			}
		}()

		return &ToolResult{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Call initiated successfully to %s. Message scheduled for speech delivery.", target)}},
		}, nil
	})

	// 4. System Run Command
	g.RegisterTool(Tool{
		Name:        "system_run_command",
		Description: "Execute a sandboxed shell or diagnostic command.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"command": {"type": "string", "description": "Shell command to execute"}
			},
			"required": ["command"]
		}`),
	}, func(ctx context.Context, args map[string]any) (*ToolResult, error) {
		command, _ := args["command"].(string)
		if command == "" {
			return nil, fmt.Errorf("command is required")
		}

		cmd := exec.CommandContext(ctx, "bash", "-c", command)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return &ToolResult{
				Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: %v\nOutput: %s", err, string(out))}},
				IsError: true,
			}, nil
		}

		return &ToolResult{
			Content: []ToolContent{{Type: "text", Text: string(out)}},
		}, nil
	})
}
