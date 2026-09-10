package monitor

import (
	"sync"
	"time"
)

type Event struct {
	Type  string  `json:"type"`
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
	Time  int64   `json:"time"`
}

type PipelineMetrics struct {
	mu          sync.RWMutex
	events      []Event
	subscribers []chan Event
}

func NewMetrics() *PipelineMetrics {
	return &PipelineMetrics{
		events: make([]Event, 0, 512),
	}
}

func (m *PipelineMetrics) Emit(name string, value float64, unit string) {
	e := Event{
		Type:  "metric",
		Name:  name,
		Value: value,
		Unit:  unit,
		Time:  time.Now().UnixMilli(),
	}

	m.mu.Lock()
	m.events = append(m.events, e)
	if len(m.events) > 500 {
		m.events = m.events[len(m.events)-256:]
	}
	subs := make([]chan Event, len(m.subscribers))
	copy(subs, m.subscribers)
	m.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (m *PipelineMetrics) EmitJSON(name string, data map[string]any) {
	payload := map[string]any{
		"type": "json",
		"name": name,
		"data": data,
		"time": time.Now().UnixMilli(),
	}
	e := Event{Type: "json", Name: name, Time: time.Now().UnixMilli()}

	m.mu.RLock()
	subs := make([]chan Event, len(m.subscribers))
	copy(subs, m.subscribers)
	m.mu.RUnlock()

	// We use the Event channel and reconstruct json at send time
	_ = payload
	for _, ch := range subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (m *PipelineMetrics) Subscribe() chan Event {
	ch := make(chan Event, 128)
	m.mu.Lock()
	m.subscribers = append(m.subscribers, ch)
	m.mu.Unlock()
	return ch
}

func (m *PipelineMetrics) Unsubscribe(ch chan Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, sub := range m.subscribers {
		if sub == ch {
			m.subscribers = append(m.subscribers[:i], m.subscribers[i+1:]...)
			break
		}
	}
	close(ch)
}

// Convenience emitters for pipeline stages

func (m *PipelineMetrics) EmitTurnStart(turn int) {
	m.Emit("turn_start", float64(turn), "turn")
}

func (m *PipelineMetrics) EmitRTPPacket(size int) {
	m.Emit("rtp_packet", float64(size), "bytes")
}

func (m *PipelineMetrics) EmitSTTDuration(ms float64) {
	m.Emit("stt_duration", ms, "ms")
}

func (m *PipelineMetrics) EmitBrainDuration(ms float64) {
	m.Emit("brain_duration", ms, "ms")
}

func (m *PipelineMetrics) EmitTTSDuration(ms float64) {
	m.Emit("tts_duration", ms, "ms")
}

func (m *PipelineMetrics) EmitTTSPlay(ms float64) {
	m.Emit("tts_play", ms, "ms")
}

func (m *PipelineMetrics) EmitTotalLatency(ms float64) {
	m.Emit("total_latency", ms, "ms")
}

func (m *PipelineMetrics) EmitBytesSent(n int) {
	m.Emit("rtp_bytes_sent", float64(n), "bytes")
}

// GetEvents returns a snapshot of all events (exported for API access)
func (m *PipelineMetrics) GetEvents() []Event {
	m.mu.RLock()
	defer m.mu.RUnlock()
	events := make([]Event, len(m.events))
	copy(events, m.events)
	return events
}
