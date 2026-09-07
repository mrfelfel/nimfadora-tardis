package tts

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/nimfadora/tardis/internal/config"
)

type EdgeTTS struct {
	cfg   config.TTSConfig
	tmpDir string
}

func New(cfg config.TTSConfig) *EdgeTTS {
	tmpDir := filepath.Join(os.TempDir(), "nimfadora-tts")
	os.MkdirAll(tmpDir, 0o755)
	return &EdgeTTS{cfg: cfg, tmpDir: tmpDir}
}

func (e *EdgeTTS) Speak(ctx context.Context, text string) ([]byte, error) {
	outPath := filepath.Join(e.tmpDir, fmt.Sprintf("tts_%d.mp3", os.Getpid()))

	args := []string{
		"--voice", e.cfg.Voice,
		"--rate=" + e.cfg.Rate,
		"--volume=" + e.cfg.Volume,
		"--write-media", outPath,
		"-t", text,
	}

	cmd := exec.CommandContext(ctx, "edge-tts", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("edge-tts failed: %w", err)
	}

	data, err := os.ReadFile(outPath)
	os.Remove(outPath)
	if err != nil {
		return nil, fmt.Errorf("read tts output: %w", err)
	}

	return data, nil
}

func (e *EdgeTTS) SpeakToFile(ctx context.Context, text, outPath string) error {
	args := []string{
		"--voice", e.cfg.Voice,
		"--rate=" + e.cfg.Rate,
		"--volume=" + e.cfg.Volume,
		"--write-media", outPath,
		"-t", text,
	}

	cmd := exec.CommandContext(ctx, "edge-tts", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
