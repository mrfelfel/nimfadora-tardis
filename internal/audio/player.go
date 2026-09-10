package audio

import (
	"io"
	"log"
	"os/exec"
	"sync"
	
)

type Player struct {
	mu        sync.Mutex
	ffmpegCmd *exec.Cmd
	ffmpegIn  io.WriteCloser
	alive     bool
}

func NewPlayer() *Player {
	return &Player{}
}

func (p *Player) ensureRunning() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.alive {
		return nil
	}

	p.ffmpegCmd = exec.Command("ffmpeg",
		"-f", "mulaw", "-ar", "8000", "-ac", "1", "-i", "pipe:0",
		"-f", "s16le", "-ar", "8000", "-ac", "1", "pipe:1",
	)
	var err error
	p.ffmpegIn, err = p.ffmpegCmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := p.ffmpegCmd.StdoutPipe()
	if err != nil {
		return err
	}
	p.ffmpegCmd.Stderr = nil
	if err := p.ffmpegCmd.Start(); err != nil {
		return err
	}
	p.alive = true

	afplay := exec.Command("afplay", "-f", "LEI16", "-r", "8000", "-c", "1", "-")
	afplay.Stdin = stdout
	afplay.Stderr = nil
	if err := afplay.Start(); err != nil {
		log.Printf("[audio] afplay: %v", err)
		p.alive = false
		return err
	}
	go func() {
		p.ffmpegCmd.Wait()
		afplay.Wait()
		p.mu.Lock()
		p.alive = false
		p.mu.Unlock()
	}()
	return nil
}

func (p *Player) PlayULaw(ulaw []byte) error {
	if len(ulaw) == 0 {
		return nil
	}
	if err := p.ensureRunning(); err != nil {
		return err
	}
	_, err := p.ffmpegIn.Write(ulaw)
	return err
}

func (p *Player) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ffmpegIn != nil {
		p.ffmpegIn.Close()
	}
	p.alive = false
}

func (p *Player) Close() {
	p.Reset()
}
