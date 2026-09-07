package stt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type GoogleSTT struct {
	apiKey string
}

func NewGoogleSTT(apiKey string) *GoogleSTT {
	return &GoogleSTT{apiKey: apiKey}
}

func (g *GoogleSTT) Transcribe(pcmData []byte) (string, error) {
	if len(pcmData) == 0 {
		return "", nil
	}

	wavPath := filepath.Join(os.TempDir(), fmt.Sprintf("stt_%d.wav", time.Now().UnixNano()))
	defer os.Remove(wavPath)

	cmd := exec.Command("ffmpeg", "-y",
		"-f", "mulaw", "-ar", "8000", "-ac", "1",
		"-i", "pipe:0",
		"-ar", "16000", "-ac", "1",
		wavPath)
	cmd.Stdin = bytes.NewReader(pcmData)
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pcm->wav: %w", err)
	}

	wavData, err := os.ReadFile(wavPath)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("https://speech.googleapis.com/v1/speech:recognize?key=%s", g.apiKey)

	reqBody := map[string]any{
		"config": map[string]any{
			"encoding":                "LINEAR16",
			"sampleRateHertz":         16000,
			"languageCode":            "fa-IR",
			"enableAutomaticPunctuation": true,
			"model":                   "latest_short",
		},
		"audio": map[string]any{
			"content": base64.StdEncoding.EncodeToString(wavData),
		},
	}

	body, _ := json.Marshal(reqBody)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("google STT: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		Results []struct {
			Alternatives []struct {
				Transcript string  `json:"transcript"`
				Confidence float64 `json:"confidence"`
			} `json:"alternatives"`
		} `json:"results"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse STT response: %w", err)
	}

	if result.Error != nil {
		return "", fmt.Errorf("google STT error %d: %s", result.Error.Code, result.Error.Message)
	}

	if len(result.Results) == 0 {
		return "", nil
	}

	text := result.Results[0].Alternatives[0].Transcript
	conf := result.Results[0].Alternatives[0].Confidence
	log.Printf("[stt] google: %q (conf=%.2f)", text, conf)
	return text, nil
}
