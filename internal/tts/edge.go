package tts

import (
	"context"
	"fmt"
	"io"
	"log"
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

// Speak generates full MP3 and converts to μ-law (legacy, blocking)
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

// SpeakToFile generates MP3 to file
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

// StreamSpeak streams μ-law audio through a channel as chunks arrive
// edge-tts → ffmpeg (mp3→μ-law) → chunks of ~160 bytes (20ms each)
func (e *EdgeTTS) StreamSpeak(ctx context.Context, text string, chunkSize int, onChunk func([]byte)) error {
	if chunkSize <= 0 {
		chunkSize = 160 // 20ms at 8kHz
	}

	// Start edge-tts writing MP3 to stdout
	edgeCmd := exec.CommandContext(ctx, "edge-tts",
		"--voice", e.cfg.Voice,
		"--rate="+e.cfg.Rate,
		"--volume="+e.cfg.Volume,
		"-t", text,
	)
	edgeStdout, err := edgeCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("edge-tts pipe: %w", err)
	}
	edgeCmd.Stderr = nil

	// Start ffmpeg: mp3 stdin → μ-law stdout
	ffmpegCmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", "pipe:0",
		"-f", "mulaw", "-ar", "8000", "-ac", "1", "pipe:1",
	)
	ffmpegCmd.Stdin = edgeStdout
	ffmpegStdout, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg pipe: %w", err)
	}
	ffmpegCmd.Stderr = nil

	if err := edgeCmd.Start(); err != nil {
		return fmt.Errorf("edge-tts start: %w", err)
	}
	if err := ffmpegCmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg start: %w", err)
	}

	// Read μ-law chunks and send via callback
	buf := make([]byte, chunkSize*4) // buffer multiple chunks
	totalSent := 0
	for {
		n, readErr := ffmpegStdout.Read(buf)
		if n > 0 {
			// Split into chunkSize pieces
			for i := 0; i < n; i += chunkSize {
				end := i + chunkSize
				if end > n {
					end = n
				}
				chunk := make([]byte, end-i)
				copy(chunk, buf[i:end])
				onChunk(chunk)
				totalSent += len(chunk)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			if ctx.Err() != nil {
				break // context cancelled
			}
			log.Printf("[tts] read error: %v", readErr)
			break
		}
	}

	edgeCmd.Wait()
	ffmpegCmd.Wait()

	log.Printf("[tts-stream] sent %d bytes (%.1fs)", totalSent, float64(totalSent)/8000)
	return nil
}

// StreamSpeakChan returns a channel that yields μ-law chunks
func (e *EdgeTTS) StreamSpeakChan(ctx context.Context, text string) (<-chan []byte, chan struct{}) {
	ch := make(chan []byte, 32)
	done := make(chan struct{})

	go func() {
		defer close(ch)
		defer close(done)

		err := e.StreamSpeak(ctx, text, 160, func(chunk []byte) {
			select {
			case ch <- chunk:
			case <-ctx.Done():
			}
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("[tts-stream] error: %v", err)
		}
	}()

	return ch, done
}
