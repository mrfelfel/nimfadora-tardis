package stt

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type VoskSTT struct {
	modelPath string
}

func NewVoskSTT(modelPath string) *VoskSTT {
	return &VoskSTT{modelPath: modelPath}
}

func (v *VoskSTT) Init() error {
	return nil
}

func (v *VoskSTT) Transcribe(pcmData []byte) (string, error) {
	start := time.Now()

	// μ-law → WAV 16kHz
	wavPath := filepath.Join(os.TempDir(), fmt.Sprintf("vosk_%d.wav", time.Now().UnixNano()))
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

	// Run vosk via Python venv
	python := "/tmp/vosk-venv/bin/python3"
	script := "/tmp/vosk_stt.py"

	out, err := exec.Command(python, script, v.modelPath, wavPath).Output()
	if err != nil {
		return "", fmt.Errorf("vosk: %w", err)
	}

	text := string(bytes.TrimSpace(out))
	elapsed := time.Since(start)
	log.Printf("[stt-vosk] %q (%.1fs)", text, elapsed.Seconds())
	return text, nil
}
