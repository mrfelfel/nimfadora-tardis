# Nimfadora TARDIS

**Autonomous AI Agent & Enterprise MCP Gateway / Control Plane**

Nimfadora TARDIS is an autonomous orchestrator and Model Context Protocol (MCP) Gateway built with Go. It serves as both a multi-agent decision engine and a unified control plane for AI agents (Claude Code, OpenCode, Cursor, and custom clients).

---

## Key Architecture

```text
Claude / OpenCode / Cursor / Web UI
              │
              ▼
   TARDIS MCP Gateway (mcpgate)
              │
      ┌───────┼──────────────────────┐
      ▼       ▼                      ▼
Coding Agent Research Agent    Telephony Tool (SIP)
(OpenCode /   (Codebase & Web) (Voice Alerts & Callouts)
Claude Code)
      │       │                      │
      └───────┼──────────────────────┘
              ▼
       Security & Governance
       • Tool Allow/Deny Policies
       • Human-in-the-Loop Approvals
       • Audit Trail & Metering Logs
       • JSON-RPC Protocol (2026-07-28)
```

---

## Features

- **MCP Gateway & Control Plane:** Exposes a unified `/mcp` JSON-RPC endpoint implementing the 2026-07-28 MCP specification.
- **Autonomous Agent Core:** Uses fast models (e.g. Mimo v2.5 / GLM-4) to analyze tasks, formulate execution plans, and invoke specialized sub-agents.
- **Delegated Coding Sub-Agents:** Offloads complex coding, debugging, and refactoring to OpenCode or Claude Code with configurable models.
- **Human-in-the-Loop Approval:** Pauses high-risk operations (such as destructive system commands or external calls) until approved by an operator via the web UI.
- **Telephony Alert Tool:** Internal SIP voice module for automated notifications and voice alerts.
- **Embedded Web UI:** Pre-built responsive dashboard for chat, approvals, tool management, and real-time audit logging.
- **Zero Heavy Dependencies:** Compiles into a single self-contained binary with embedded assets (`embed.FS`).

---

## Quick Start

### 1. Configuration

Copy the example configuration:

```bash
cp config.example.yaml config.yaml
```

Set environment variables or edit `config.yaml`:

```bash
export BRAIN_API_KEY="your-api-key"
```

### 2. Build & Run

```bash
# Build binary
go build -o tardis ./cmd/nimfadora

# Start TARDIS Gateway and Web UI
./tardis
```

Open your browser at:
- **Web UI & Chat:** `http://localhost:8080`
- **MCP Endpoint:** `http://localhost:8080/mcp`
- **Metrics Dashboard:** `http://localhost:8199`

---

## MCP Gateway Integration

Connect any MCP-compatible agent to TARDIS:

### Claude Desktop / Cursor Config

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

## Vercel Deployment

Deploy the MCP Gateway web dashboard and serverless endpoints to Vercel:

```bash
vercel --prod
```

---

## License

MIT License.
