# Nimfadora TARDIS

**Autonomous AI Agent & MCP Gateway / Control Plane**

[![GitHub](https://img.shields.io/badge/GitHub-mrfelfel-blue?logo=github)](https://github.com/mrfelfel/nimfadora-tardis)
[![Protocol](https://img.shields.io/badge/MCP-2026--07--28-purple)](https://modelcontextprotocol.io)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)](https://go.dev)

Nimfadora TARDIS is an autonomous multi-agent orchestrator and Model Context Protocol (MCP) Gateway built in Go. It combines an AI decision engine with a unified control plane -- configure everything from the web dashboard, run a task, watch the agent plan and execute step by step.

---

## Architecture

```text
    Web UI / Browser
         |
         v
  TARDIS Control Plane (Go binary)
         |
   +-----+-------------------+
   |                         |
   v                         v
 Autonomous Agent      MCP Gateway (/mcp)
 (brain decides)       (JSON-RPC 2026-07-28)
   |                         |
   +-----+-----+-----+------+
   |     |     |     |      |
   v     v     v     v      v
  Code  Research  SIP   System
 Agent  Agent   Voice   Shell
   |     |       |       |
   +-----+-------+-------+
              |
              v
      Policy / Approvals
      Audit Trail / Logs
```

---

## Features

- **Full Web Dashboard** -- all settings (brain, models, API keys, SIP, TTS, tool policies) configurable from the Settings tab. No config files needed after first run.
- **MCP Gateway** -- standard `/mcp` endpoint with JSON-RPC 2026-07-28, ready for Cursor, OpenCode, or any MCP-compatible client.
- **Autonomous Agent** -- uses fast models (Mimo v2.5, GLM-4, DeepSeek, etc.) to analyze tasks and invoke the right tool.
- **Coding Agent** -- delegates programming tasks to OpenCode with your own API key and model.
- **Research Agent** -- searches codebase, documentation, and architecture.
- **SIP Telephony** -- place voice calls and deliver alerts over SIP/Navaphone.
- **Human Approval** -- dangerous operations pause and wait for operator approval in the UI.
- **Audit Trail** -- every tool call logged with agent, duration, approval status, and output sample.
- **Single Binary** -- one self-contained executable, embedded web UI, no runtime dependencies.

---

## Quick Start

### 1. Build

Requires Go 1.21+.

```bash
git clone https://github.com/mrfelfel/nimfadora-tardis.git
cd nimfadora-tardis
go build -o tardis ./cmd/nimfadora
```

### 2. Run

```bash
./tardis
```

Open **http://localhost:8080** in your browser.

### 3. Configure via Dashboard

Click the **Settings** tab and fill in:

| Section | Fields |
|---------|--------|
| **Brain / LLM** | Base URL, API Key, Model, Max Tokens, Temperature |
| **MCP Gateway** | Coding backend (OpenCode / Bash), coding model, approval list, denied tools |
| **SIP / Telephony** | Host, port, username, password, caller ID, transport |
| **Text-to-Speech** | Voice, rate, volume |

Press **Save Settings** -- applied instantly, no restart needed.

---

## MCP Gateway Integration

Connect TARDIS as an MCP server for any compatible client:

```json
{
  "mcpServers": {
    "tardis": {
      "url": "http://localhost:8080/mcp"
    }
  }
}
```

---

## Project Structure

```text
cmd/nimfadora/main.go          Entry point
internal/
  agent/agent.go               Autonomous agent loop
  brain/mimo.go                LLM client (OpenAI-compatible)
  call/handler.go              SIP call handler
  mcpgate/
    gateway.go                 MCP Gateway core + policy engine
    server.go                  HTTP server + Settings API
    tools.go                   Built-in tool registry
    types.go                   MCP protocol types
    dashboard.html             Embedded Web UI
  sip/client.go                SIP client (sipgo)
  stt/                         Speech-to-text (Vosk)
  tts/edge.go                  Text-to-speech (Edge TTS)
  config/config.go             Configuration store
vercel/                        Vercel serverless entry point (optional)
```

---

## License

MIT
