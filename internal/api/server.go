package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/nimfadora/tardis/internal/monitor"
)

type CallRequest struct {
	Target   string `json:"target"`
	From     string `json:"from"`
	Greeting string `json:"greeting"`
}

type NotifyRequest struct {
	Target  string `json:"target"`
	From    string `json:"from"`
	Message string `json:"message"`
}

type NotifyStatus struct {
	Running bool   `json:"running"`
	Target  string `json:"target"`
	Message string `json:"message"`
	Started string `json:"started"`
	Error   string `json:"error,omitempty"`
}

type CallStatus struct {
	Running bool   `json:"running"`
	Target  string `json:"target"`
	From    string `json:"from"`
	Turns   int    `json:"turns"`
	Started string `json:"started"`
	Error   string `json:"error,omitempty"`
}

type Server struct {
	metrics      *monitor.PipelineMetrics
	status       CallStatus
	notifyStatus NotifyStatus
	mu           sync.RWMutex
	startCall    func(ctx context.Context, req CallRequest) error
	startNotify  func(ctx context.Context, req NotifyRequest) error
	cancelCall   context.CancelFunc
	cancelNotify context.CancelFunc
}

func NewServer(metrics *monitor.PipelineMetrics) *Server {
	return &Server{metrics: metrics}
}

func (s *Server) SetCallHandler(fn func(ctx context.Context, req CallRequest) error) {
	s.startCall = fn
}

func (s *Server) SetNotifyHandler(fn func(ctx context.Context, req NotifyRequest) error) {
	s.startNotify = fn
}

func (s *Server) Start(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/call", s.handleStartCall)
	mux.HandleFunc("POST /api/call/stop", s.handleStopCall)
	mux.HandleFunc("POST /api/notify", s.handleNotify)
	mux.HandleFunc("POST /api/notify/stop", s.handleStopNotify)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/metrics", s.handleMetrics)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	log.Printf("[api] REST: http://%s", addr)
	go http.ListenAndServe(addr, mux)
}

func (s *Server) handleStartCall(w http.ResponseWriter, r *http.Request) {
	var req CallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	s.mu.Lock()
	if s.status.Running {
		s.mu.Unlock()
		http.Error(w, "call already running", 409)
		return
	}
	if req.Target == "" {
		s.mu.Unlock()
		http.Error(w, "target required", 400)
		return
	}
	if s.startCall == nil {
		s.mu.Unlock()
		http.Error(w, "handler not ready", 503)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelCall = cancel
	s.status = CallStatus{Running: true, Target: req.Target, From: req.From, Started: time.Now().Format(time.RFC3339)}
	s.mu.Unlock()
	go func() {
		err := s.startCall(ctx, req)
		s.mu.Lock()
		s.status.Running = false
		if err != nil {
			s.status.Error = err.Error()
		}
		s.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

func (s *Server) handleStopCall(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.status.Running {
		http.Error(w, "no active call", 404)
		return
	}
	if s.cancelCall != nil {
		s.cancelCall()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopping"})
}

func (s *Server) handleNotify(w http.ResponseWriter, r *http.Request) {
	var req NotifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	s.mu.Lock()
	if s.notifyStatus.Running {
		s.mu.Unlock()
		http.Error(w, "notify already running", 409)
		return
	}
	if req.Target == "" || req.Message == "" {
		s.mu.Unlock()
		http.Error(w, "target and message required", 400)
		return
	}
	if s.startNotify == nil {
		s.mu.Unlock()
		http.Error(w, "handler not ready", 503)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	s.cancelNotify = cancel
	s.notifyStatus = NotifyStatus{Running: true, Target: req.Target, Message: req.Message, Started: time.Now().Format(time.RFC3339)}
	s.mu.Unlock()
	go func() {
		err := s.startNotify(ctx, req)
		s.mu.Lock()
		s.notifyStatus.Running = false
		if err != nil {
			s.notifyStatus.Error = err.Error()
		}
		s.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "sending"})
}

func (s *Server) handleStopNotify(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.notifyStatus.Running {
		http.Error(w, "no active notify", 404)
		return
	}
	if s.cancelNotify != nil {
		s.cancelNotify()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopping"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.status)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	events := s.metrics.GetEvents()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"events": events})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "ok")
}
