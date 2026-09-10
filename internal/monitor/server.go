package monitor

import (
	"embed"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

//go:embed dashboard.html
var dashboardFS embed.FS

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Server struct {
	metrics *PipelineMetrics
	clients map[*websocket.Conn]bool
	mu      sync.RWMutex
}

func NewServer(metrics *PipelineMetrics) *Server {
	return &Server{
		metrics: metrics,
		clients: make(map[*websocket.Conn]bool),
	}
}

func (s *Server) Start(addr string) {
	mux := http.NewServeMux()

	// Dashboard HTML
	mux.HandleFunc("/", s.handleDashboard)

	// WebSocket endpoint for real-time metrics
	mux.HandleFunc("/ws", s.handleWS)

	// REST: current metrics snapshot
	mux.HandleFunc("/api/metrics", s.handleMetricsAPI)

	log.Printf("[monitor] dashboard: http://%s", addr)
	go http.ListenAndServe(addr, mux)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data, err := dashboardFS.ReadFile("dashboard.html")
	if err != nil {
		http.Error(w, "dashboard not found", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[monitor] ws upgrade: %v", err)
		return
	}

	s.mu.Lock()
	s.clients[conn] = true
	s.mu.Unlock()

	log.Printf("[monitor] client connected (%d total)", len(s.clients))

	ch := s.metrics.Subscribe()
	defer func() {
		s.metrics.Unsubscribe(ch)
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
		conn.Close()
		log.Printf("[monitor] client disconnected (%d total)", len(s.clients))
	}()

	// Send initial history
	s.mu.RLock()
	history := make([]Event, len(s.metrics.events))
	copy(history, s.metrics.events)
	s.mu.RUnlock()

	if len(history) > 0 {
		j, _ := json.Marshal(map[string]any{"type": "history", "events": history})
		conn.WriteMessage(websocket.TextMessage, j)
	}

	// Forward new events
	for event := range ch {
		j, err := json.Marshal(event)
		if err != nil {
			continue
		}
		conn.WriteMessage(websocket.TextMessage, j)
	}
}

func (s *Server) handleMetricsAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	events := make([]Event, len(s.metrics.events))
	copy(events, s.metrics.events)
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"events": events})
}
