package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/nimfadora/tardis/internal/brain"
	"github.com/nimfadora/tardis/internal/mcpgate"
)

// TardisAgent orchestrates thinking, multi-agent dispatch, and tool execution
type TardisAgent struct {
	brain   *brain.Mimo
	gateway *mcpgate.Gateway
	history []brain.Message
}

func New(brainClient *brain.Mimo, gateway *mcpgate.Gateway) *TardisAgent {
	return &TardisAgent{
		brain:   brainClient,
		gateway: gateway,
		history: make([]brain.Message, 0),
	}
}

type AgentPlan struct {
	Thought string         `json:"thought"`
	Action  string         `json:"action"` // "tool" or "final_answer"
	Tool    string         `json:"tool,omitempty"`
	Args    map[string]any `json:"args,omitempty"`
	Answer  string         `json:"answer,omitempty"`
}

const systemPrompt = `You are Nimfadora TARDIS, an advanced autonomous AI agent and orchestrator.
You have access to an internal MCP Gateway with tools:
- coding_agent: delegates coding, debugging, refactoring, or programming to an AI coding tool (like OpenCode, Claude Code) with a chosen model (e.g. glm-4, deepseek-coder).
- research_agent: does deep research and investigation across the codebase or topic.
- telephony_make_call: places an actual phone call to deliver a voice notification over SIP.
- system_run_command: runs a shell command on the machine.

When a user asks you to do something:
1. Break down the problem and decide whether you need an external coding agent, research, phone alert, or direct response.
2. If you need a tool, respond ONLY in strict JSON format:
{
  "thought": "Reasoning about what to do next",
  "action": "tool",
  "tool": "tool_name",
  "args": {"param1": "value1"}
}

3. If you have the final answer or completed the request, respond ONLY in strict JSON format:
{
  "thought": "All tasks are resolved",
  "action": "final_answer",
  "answer": "Your detailed answer or summary to the user in the language they used (Persian or English)."
}

Never output markdown codeblocks around the JSON. Output valid raw JSON only.`

// Run executes the agent loop until a final answer or max iterations is reached
func (a *TardisAgent) Run(ctx context.Context, userQuery string) (string, error) {
	log.Printf("[agent] received query: %s", userQuery)

	a.history = append(a.history, brain.Message{
		Role:    "user",
		Content: userQuery,
	})

	maxTurns := 6
	for turn := 0; turn < maxTurns; turn++ {
		log.Printf("[agent] turn %d thinking...", turn+1)

		reply, err := a.brain.Chat(ctx, systemPrompt, a.history)
		if err != nil {
			return "", fmt.Errorf("brain chat error: %w", err)
		}

		reply = strings.TrimSpace(reply)
		// Clean markdown wrappers if model put them
		reply = strings.TrimPrefix(reply, "```json")
		reply = strings.TrimPrefix(reply, "```")
		reply = strings.TrimSuffix(reply, "```")
		reply = strings.TrimSpace(reply)

		var plan AgentPlan
		if err := json.Unmarshal([]byte(reply), &plan); err != nil {
			// If not valid JSON, treat as direct answer
			log.Printf("[agent] response not strict JSON, using raw reply")
			a.history = append(a.history, brain.Message{
				Role:    "assistant",
				Content: reply,
			})
			return reply, nil
		}

		log.Printf("[agent] plan action='%s', thought='%s'", plan.Action, plan.Thought)

		if plan.Action == "final_answer" {
			a.history = append(a.history, brain.Message{
				Role:    "assistant",
				Content: plan.Answer,
			})
			return plan.Answer, nil
		}

		if plan.Action == "tool" {
			log.Printf("[agent] executing tool: %s with args: %+v", plan.Tool, plan.Args)
			toolRes, err := a.gateway.CallTool(ctx, "nimfadora-tardis", plan.Tool, plan.Args)

			resultText := ""
			if err != nil {
				resultText = fmt.Sprintf("Error calling tool: %v", err)
			} else if toolRes != nil && len(toolRes.Content) > 0 {
				resultText = toolRes.Content[0].Text
			}

			log.Printf("[agent] tool result preview: %.100s...", resultText)

			// Add observation back to history
			a.history = append(a.history, brain.Message{
				Role:    "assistant",
				Content: reply,
			})
			a.history = append(a.history, brain.Message{
				Role:    "user",
				Content: fmt.Sprintf("Tool %s result:\n%s", plan.Tool, resultText),
			})

			// Wait slightly between turns
			time.Sleep(200 * time.Millisecond)
		}
	}

	return "Reached maximum autonomous reasoning steps. Please inspect the logs or refine the prompt.", nil
}
