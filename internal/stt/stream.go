package stt

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type StreamSTT struct {
	modelPath string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *bufio.Reader
	mu        sync.Mutex
}

func NewStreamSTT(modelPath string) *StreamSTT {
	return &StreamSTT{modelPath: modelPath}
}

func (s *StreamSTT) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cmd = exec.Command("/opt/homebrew/bin/whisper-stream",
		"-m", s.modelPath,
		"--language", "fa",
		"--step", "1000",
		"--length", "5000",
		"-t", "4",
		"--no-prints",
	)

	var err error
	s.stdin, err = s.cmd.StdinPipe()
	if err != nil {
		return err
	}

	stdout, err := s.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	s.stdout = bufio.NewReader(stdout)
	s.cmd.Stderr = nil

	return s.cmd.Start()
}

func (s *StreamSTT) SendPCM(pcmData []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stdin == nil {
		return fmt.Errorf("not started")
	}
	_, err := s.stdin.Write(pcmData)
	return err
}

func (s *StreamSTT) ReadTranscript(timeout time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stdout == nil {
		return "", fmt.Errorf("not started")
	}

	deadline := time.Now().Add(timeout)
	var lines []string

	for time.Now().Before(deadline) {
		line, err := s.stdout.ReadString('\n')
		if err != nil {
			if len(lines) > 0 {
				break
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}

	if len(lines) == 0 {
		return "", nil
	}

	result := lines[len(lines)-1]
	if idx := strings.Index(result, "]"); idx >= 0 && idx < len(result)-1 {
		result = strings.TrimSpace(result[idx+1:])
	}

	log.Printf("[stt-stream] %q", result)
	return result, nil
}

func (s *StreamSTT) Transcribe(pcmData []byte) (string, error) {
	if err := s.SendPCM(pcmData); err != nil {
		return "", err
	}
	return s.ReadTranscript(10 * time.Second)
}

func (s *StreamSTT) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stdin != nil {
		s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
}
