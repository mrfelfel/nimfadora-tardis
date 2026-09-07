package stt

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Whisper struct {
	modelPath string
}

func NewWhisper(modelPath string) *Whisper {
	return &Whisper{modelPath: modelPath}
}

func (w *Whisper) Transcribe(pcmData []byte) (string, error) {
	if len(pcmData) == 0 {
		return "", nil
	}

	wavPath := filepath.Join(os.TempDir(), fmt.Sprintf("stt_%d.wav", os.Getpid()))
	defer os.Remove(wavPath)

	cmd := exec.Command("ffmpeg", "-y",
		"-f", "mulaw", "-ar", "8000", "-ac", "1",
		"-i", "pipe:0",
		"-af", "volume=2.0",
		"-ar", "16000", "-ac", "1",
		wavPath)
	cmd.Stdin = bytes.NewReader(pcmData)
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pcm->wav: %w", err)
	}

	out, err := exec.Command("/opt/homebrew/bin/whisper-cli",
		"-m", w.modelPath,
		"-f", wavPath,
		"--language", "fa",
		"--no-prints",
		"--beam-size", "3",
		"--best-of", "1",
		"-t", "4",
	).CombinedOutput()
	if err != nil {
		log.Printf("[stt] whisper error: %v\n%s", err, string(out))
		return "", fmt.Errorf("whisper: %w", err)
	}

	text := extractLastLine(string(out))
	text = strings.TrimSpace(text)
	log.Printf("[stt] transcribed: %q", text)
	return text, nil
}

func extractLastLine(output string) string {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		// Strip timestamp prefix like "[00:00:00.000 --> 00:00:02.000]  text"
		if idx := strings.Index(l, "]"); idx >= 0 && idx < len(l)-1 {
			l = strings.TrimSpace(l[idx+1:])
		}
		// Skip whisper info lines
		if strings.HasPrefix(l, "output_") || strings.HasPrefix(l, "load_") || strings.HasPrefix(l, "ggml") {
			continue
		}
		return l
	}
	return ""
}
