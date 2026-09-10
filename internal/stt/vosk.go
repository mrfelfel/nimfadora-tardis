package stt

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type VoskSTT struct {
	modelPath string
	cmd       *exec.Cmd
	stdin     *bufio.Writer
	stdout    *bufio.Scanner
	mu        sync.Mutex
}

func NewVoskSTT(modelPath string) *VoskSTT {
	return &VoskSTT{modelPath: modelPath}
}

func (v *VoskSTT) Init() error {
	python := "/tmp/vosk-venv/bin/python3"
	script := "/tmp/whisper_server.py"

	v.cmd = exec.Command(python, "-u", script)
	v.cmd.Stderr = os.Stderr

	stdinPipe, err := v.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("whisper stdin: %w", err)
	}
	stdoutPipe, err := v.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("whisper stdout: %w", err)
	}

	v.stdin = bufio.NewWriter(stdinPipe)
	v.stdout = bufio.NewScanner(stdoutPipe)

	if err := v.cmd.Start(); err != nil {
		return fmt.Errorf("whisper start: %w", err)
	}

	// Wait for "model ready" message
	log.Println("[stt] whisper server starting...")
	for v.stdout.Scan() {
		line := v.stdout.Text()
		if line == "[whisper-server] model ready!" {
			log.Println("[stt] whisper server ready")
			return nil
		}
	}

	return fmt.Errorf("whisper server failed to start")
}

func (v *VoskSTT) Transcribe(pcmData []byte) (string, error) {
	start := time.Now()

	// μ-law → WAV 16kHz
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
		return "", fmt.Errorf("pcm convert: %w", err)
	}

	// Send to persistent server
	v.mu.Lock()
	defer v.mu.Unlock()

	req, _ := json.Marshal(map[string]string{"path": wavPath})
	fmt.Fprintf(v.stdin, "%s\n", req)
	v.stdin.Flush()

	// Read response
	if !v.stdout.Scan() {
		return "", fmt.Errorf("whisper server closed")
	}

	var result struct {
		Text  string `json:"text"`
		Time  float64 `json:"time"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(v.stdout.Text()), &result); err != nil {
		return "", fmt.Errorf("whisper parse: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("whisper: %s", result.Error)
	}

	elapsed := time.Since(start)
	log.Printf("[stt-whisper] %q (%.1fs server, %.1fs total)", result.Text, result.Time, elapsed.Seconds())
	return result.Text, nil
}
