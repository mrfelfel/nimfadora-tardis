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
