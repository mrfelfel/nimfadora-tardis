package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/nimfadora/tardis/internal/config"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Mimo struct {
	cfg    config.BrainConfig
	client *http.Client
}

func New(cfg config.BrainConfig) *Mimo {
	return &Mimo{
		cfg:    cfg,
		client: &http.Client{},
	}
}

func (m *Mimo) Chat(ctx context.Context, systemPrompt string, messages []Message) (string, error) {
	msgs := make([]map[string]string, 0, len(messages)+1)
	if systemPrompt != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": systemPrompt})
	}
	for _, msg := range messages {
		msgs = append(msgs, map[string]string{"role": msg.Role, "content": msg.Content})
	}

	body := map[string]any{
		"model":       m.cfg.Model,
		"messages":    msgs,
		"max_tokens":  m.cfg.MaxTokens,
		"temperature": m.cfg.Temperature,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", m.cfg.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.cfg.APIKey)

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("brain request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("brain returned %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode brain response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("brain returned no choices")
	}

	return result.Choices[0].Message.Content, nil
}

func (m *Mimo) SimpleChat(ctx context.Context, userMessage string) (string, error) {
	return m.Chat(ctx, "", []Message{{Role: "user", Content: userMessage}})
}

// ChatStream streams brain tokens via channel using OpenAI streaming API.
// Returns a read channel of text chunks; caller should range over it until closed.
func (m *Mimo) ChatStream(ctx context.Context, systemPrompt string, messages []Message) (<-chan string, chan struct{}) {
	msgs := make([]map[string]string, 0, len(messages)+1)
	if systemPrompt != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": systemPrompt})
	}
	for _, msg := range messages {
		msgs = append(msgs, map[string]string{"role": msg.Role, "content": msg.Content})
	}

	body := map[string]any{
		"model":       m.cfg.Model,
		"messages":    msgs,
		"max_tokens":  m.cfg.MaxTokens,
		"temperature": m.cfg.Temperature,
		"stream":      true,
	}

	data, err := json.Marshal(body)
	if err != nil {
		ch := make(chan string, 1)
		close(ch)
		return ch, nil
	}

	req, err := http.NewRequestWithContext(ctx, "POST", m.cfg.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		ch := make(chan string, 1)
		close(ch)
		return ch, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.cfg.APIKey)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := m.client.Do(req)
	if err != nil {
		ch := make(chan string, 1)
		close(ch)
		return ch, nil
	}

	ch := make(chan string, 64)
	done := make(chan struct{})

	go func() {
		defer close(ch)
		defer close(done)
		defer resp.Body.Close()

		scanner := newLineScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "[DONE]" || line == "" {
				continue
			}
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(line), &chunk); err != nil {
				continue
			}
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				select {
				case ch <- chunk.Choices[0].Delta.Content:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, done
}

// lineScanner reads lines from io.Reader (SSE format: "data: {...}" or "data: [DONE]")
func newLineScanner(r io.Reader) *sseLineScanner {
	return &sseLineScanner{buf: make([]byte, 4096), reader: r}
}

type sseLineScanner struct {
	buf         []byte
	pos         int
	n           int
	reader      io.Reader
	done        bool
	currentLine string
}

func (s *sseLineScanner) Text() string { return s.currentLine }

func (s *sseLineScanner) Scan() bool {
	for {
		// Find end of current line in buffer
		for i := s.pos; i < s.n; i++ {
			if s.buf[i] == '\n' {
				line := string(s.buf[s.pos:i])
				s.pos = i + 1
				// Strip "data: " prefix if present
				if len(line) >= 6 && line[:6] == "data: " {
					line = line[6:]
				}
				s.currentLine = line
				return true
			}
		}
		// Need more data
		if s.done {
			return false
		}
		// Compact buffer
		if s.pos > 0 {
			copy(s.buf, s.buf[s.pos:s.n])
			s.n -= s.pos
			s.pos = 0
		}
		n, err := s.reader.Read(s.buf[s.n:])
		s.n += n
		if err != nil {
			s.done = true
			if s.pos < s.n {
				line := string(s.buf[s.pos:s.n])
				s.pos = s.n
				if len(line) >= 6 && line[:6] == "data: " {
					line = line[6:]
				}
				s.currentLine = line
				return true
			}
			return false
		}
	}
}
