# Nimfadora TARDIS

**Autonomous AI Agent & Enterprise MCP Gateway / Control Plane**

[![GitHub](https://img.shields.io/badge/GitHub-Repository-blue?logo=github)](https://github.com/mrfelfel/nimfadora-tardis)
[![Protocol](https://img.shields.io/badge/MCP-2026--07--28-purple)](https://modelcontextprotocol.io)

Nimfadora TARDIS is an autonomous orchestrator and Model Context Protocol (MCP) Gateway built with Go. It serves as both a multi-agent decision engine and a unified control plane for AI agents (Claude Code, OpenCode, Cursor, and custom clients).

All configuration is managed through the built-in Web UI -- no config files needed after initial setup.

---

## Key Architecture

```text
Claude / OpenCode / Cursor / Web UI
              |
              v
   TARDIS MCP Gateway (mcpgate)
              |
      +-------+----------------------+
      v       v                      v
Coding Agent Research Agent    Telephony Tool (SIP)
(OpenCode /   (Codebase & Web) (Voice Alerts & Callouts)
Claude Code)
      |       |                      |
      +-------+----------------------+
              v
       Security & Governance
       - Tool Allow/Deny Policies
       - Human-in-the-Loop Approvals
       - Audit Trail & Metering Logs
       - JSON-RPC Protocol (2026-07-28)
```

---

## Features

- **Full Web Control Plane:** Configure brain models, API keys, SIP telephony, tool policies, and coding backends -- all from the Settings tab in the dashboard.
- **MCP Gateway & Control Plane:** Exposes a unified `/mcp` JSON-RPC endpoint implementing the 2026-07-28 MCP specification.
- **Autonomous Agent Core:** Uses fast models (e.g. Mimo v2.5 / GLM-4) to analyze tasks, formulate execution plans, and invoke specialized sub-agents.
- **Delegated Coding Sub-Agents:** Offloads complex coding to OpenCode or Claude Code with configurable models.
- **Human-in-the-Loop Approval:** Pauses high-risk operations until approved by an operator via the web UI.
- **Telephony Alert Tool:** Internal SIP voice module for automated notifications and voice alerts.
- **Audit Trail:** Every tool call is logged with agent, duration, approval status, and output.
- **Single Binary:** Compiles into one self-contained binary with embedded assets (`embed.FS`).

---

## Quick Start

### 1. Build

```bash
go build -o tardis ./cmd/nimfadora
```

### 2. Run

```bash
./tardis
```

### 3. Configure via Web UI

Open **http://localhost:8080** and click the **Settings** tab:

| Section | What to configure |
|---------|-------------------|
| **Brain / LLM** | API base URL, API key, model name, max tokens, temperature |
| **Coding Agent** | Backend (OpenCode / Claude Code / Bash) and model override |
| **Security & Policy** | Tools requiring approval, denied tools, dangerous mode toggle |
| **Telephony / SIP** | SIP host, port, username, password, caller ID, transport |
| **Text-to-Speech** | Voice name, rate, volume (Edge TTS) |

Settings are saved to `config.yaml` and applied immediately at runtime -- no restart needed.

---

## Deployment

### Local / Self-Hosted

```bash
# Build for your platform
go build -o tardis ./cmd/nimfadora

# Run
./tardis --addr 0.0.0.0:8080
```

For production, run behind a reverse proxy (nginx, Caddy) with TLS:

```nginx
# nginx example
server {
    listen 443 ssl;
    server_name tardis.yourdomain.com;

    ssl_certificate     /etc/letsencrypt/live/tardis.yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/tardis.yourdomain.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

### Vercel (Static Dashboard Only)

The `public/` directory contains a static copy of the dashboard UI.
Vercel can serve it, but the full agent/mcp functionality requires the Go binary.

```bash
vercel --prod
```

### Docker

```bash
FROM golang:1.25-alpine AS build
WORKDIR /app
COPY . .
RUN go build -o tardis ./cmd/nimfadora

FROM alpine:3.19
COPY --from=build /app/tardis /usr/local/bin/tardis
EXPOSE 8080
CMD ["tardis", "--addr", "0.0.0.0:8080"]
```

---

## MCP Gateway Integration

Connect any MCP-compatible agent to TARDIS:

### Claude Desktop / Cursor / OpenCode Config

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
cmd/nimfadora/main.go        Entry point
internal/
  agent/agent.go             Autonomous multi-agent orchestrator
  brain/mimo.go              LLM client (OpenAI-compatible API)
  call/handler.go            SIP call handler (experimental conversation)
  mcpgate/
    gateway.go               MCP Gateway core + policy engine
    server.go                HTTP server + settings API
    tools.go                 Built-in tool registry
    types.go                 MCP protocol types
    dashboard.html           Embedded Web UI
  sip/client.go              SIP client (sipgo)
  stt/                       Speech-to-text (Vosk/Whisper)
  tts/edge.go                Text-to-speech (Edge TTS)
  config/config.go           Configuration + live settings store
```

---

## License

MIT License.
