// Package api provides the Vercel Serverless Go handler for the TARDIS Control Plane.
// It serves the full dashboard UI and all API endpoints from a single serverless function.
package api

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed dashboard.html
var dashboardHTML []byte

var (
	settingsMu sync.RWMutex
	settings   = map[string]any{
		"brain": map[string]any{
			"base_url":     "https://api.openai.com/v1",
			"api_key":      "",
			"model":        "gpt-4o",
			"max_tokens":   256,
			"temperature":  0.7,
		},
		"gateway": map[string]any{
			"allow_dangerous":  false,
			"require_approval": []any{"telephony_make_call", "system_run_command"},
			"allowed_tools":    []any{},
			"denied_tools":     []any{},
			"coding_backend":   "opencode",
			"coding_model":     "glm-4",
			"research_backend": "builtin",
		},
		"sip": map[string]any{
			"host":        "voice.navaphone.com",
			"port":        5060,
			"username":    "",
			"password":    "",
			"from_number": "",
			"transport":   "udp",
		},
		"tts": map[string]any{
			"voice":  "en-US-GuyNeural",
			"rate":   "+0%",
			"volume": "+0%",
		},
	}
)

func init() {
	loadFromDisk()
}

func loadFromDisk() {
	path := "config.yaml"
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var loaded map[string]any
	if err := yaml.Unmarshal(data, &loaded); err == nil {
		settingsMu.Lock()
		for k, v := range loaded {
			settings[k] = v
		}
		settingsMu.Unlock()
	}
}

func saveToDisk() error {
	data, err := yaml.Marshal(settings)
	if err != nil {
		return err
	}
	return os.WriteFile("config.yaml", data, 0644)
}

// Handler is the Vercel serverless entry point.
// Every route under /api/* is routed here via vercel.json rewrites.
func Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	path := r.URL.Path

	switch {
	case path == "/api/settings" || path == "/api/settings/":
		handleSettings(w, r)
	case path == "/api/audit" || path == "/api/audit/":
		handleAuditLogs(w, r)
	case path == "/api/tools" || path == "/api/tools/":
		handleTools(w, r)
	case path == "/" || path == "":
		serveDashboard(w, r)
	default:
		http.NotFound(w, r)
	}
}

func serveDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(dashboardHTML)
}

func handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settingsMu.RLock()
		data := settings
		settingsMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)

	case http.MethodPut:
		var incoming map[string]any
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		settingsMu.Lock()
		for k, v := range incoming {
			settings[k] = v
		}
		err := saveToDisk()
		settingsMu.Unlock()

		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Settings saved and applied"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleAuditLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode([]any{})
}

func handleTools(w http.ResponseWriter, r *http.Request) {
	tools := []map[string]any{
		{"name": "coding_agent", "description": "Delegate programming tasks to OpenCode / Claude Code with a chosen model."},
		{"name": "research_agent", "description": "Perform deep research across codebase and documentation."},
		{"name": "telephony_make_call", "description": "Place an outbound SIP voice call to deliver a notification."},
		{"name": "system_run_command", "description": "Execute a shell command on the machine."},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"tools": tools, "count": len(tools)})
}
