package mcpgate

import (
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/nimfadora/tardis/internal/config"
)

//go:embed dashboard.html
var dashboardHTML []byte

type AgentRunner interface {
	Run(ctx context.Context, query string) (string, error)
}

type Server struct {
	gateway  *Gateway
	agent    AgentRunner
	settings *config.SettingsStore
}

func NewServer(gateway *Gateway, agent AgentRunner, settings *config.SettingsStore) *Server {
	return &Server{
		gateway:  gateway,
		agent:    agent,
		settings: settings,
	}
}

func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()

	// Web UI
	mux.HandleFunc("/", s.handleDashboard)

	// Agent Chat API
	mux.HandleFunc("/api/agent/chat", s.handleAgentChat)

	// Settings API
	mux.HandleFunc("/api/settings", s.handleSettings)

	// MCP Gateway Endpoints (Protocol 2026-07-28 & JSON-RPC)
	mux.HandleFunc("/mcp", s.handleMCP)
	mux.HandleFunc("/api/tools", s.handleListTools)
	mux.HandleFunc("/api/tools/call", s.handleCallTool)
	mux.HandleFunc("/api/approvals", s.handleApprovals)
	mux.HandleFunc("/api/approvals/resolve", s.handleResolveApproval)
	mux.HandleFunc("/api/audit", s.handleAuditLogs)

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	log.Printf("[mcpgate] Control Plane & Agent Web UI running on http://%s", addr)
	return srv.ListenAndServe()
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	agentID := r.Header.Get("X-Agent-ID")
	if agentID == "" {
		agentID = "external-agent"
	}

	resp, err := s.gateway.HandleJSONRPC(r.Context(), agentID, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings := s.settings.Get()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(settings)

	case http.MethodPut:
		var incoming config.Settings
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.settings.Set(incoming); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		// Apply gateway policy changes immediately
		gwCfg := config.GatewayConfig{
			AllowDangerous:  incoming.Gateway.AllowDangerous,
			RequireApproval: incoming.Gateway.RequireApproval,
			AllowedTools:    incoming.Gateway.AllowedTools,
			DeniedTools:     incoming.Gateway.DeniedTools,
			CodingBackend:   incoming.Gateway.CodingBackend,
			CodingModel:     incoming.Gateway.CodingModel,
		}
		s.gateway.SetConfig(gwCfg)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Settings saved and applied"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	tools := s.gateway.ListTools()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"tools": tools,
		"count": len(tools),
	})
}

func (s *Server) handleCallTool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		AgentID string         `json:"agent_id"`
		Name    string         `json:"name"`
		Args    map[string]any `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.AgentID == "" {
		req.AgentID = "web-ui"
	}

	res, err := s.gateway.CallTool(r.Context(), req.AgentID, req.Name, req.Args)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (s *Server) handleApprovals(w http.ResponseWriter, r *http.Request) {
	approvals := s.gateway.GetApprovals()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(approvals)
}

func (s *Server) handleResolveApproval(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID      string `json:"id"`
		Approve bool   `json:"approve"`
		User    string `json:"user"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.User == "" {
		req.User = "operator"
	}

	res, err := s.gateway.ResolveApproval(req.ID, req.Approve, req.User)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (s *Server) handleAuditLogs(w http.ResponseWriter, r *http.Request) {
	logs := s.gateway.GetAuditLogs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (s *Server) handleAgentChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if s.agent == nil {
		http.Error(w, "Agent not configured with LLM Brain", http.StatusServiceUnavailable)
		return
	}

	answer, err := s.agent.Run(r.Context(), req.Query)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"answer": answer,
	})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(dashboardHTML)
}
