package call

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"time"

	"github.com/nimfadora/tardis/internal/brain"
	"github.com/nimfadora/tardis/internal/config"
	"github.com/nimfadora/tardis/internal/rtp"
	"github.com/nimfadora/tardis/internal/sip"
	"github.com/nimfadora/tardis/internal/stt"
	"github.com/nimfadora/tardis/internal/tts"
)

type Handler struct {
	sipClient *sip.Client
	tts       *tts.EdgeTTS
	stt       *stt.VoskSTT
	brain     *brain.Mimo
	cfg       *config.Config
}

func New(cfg *config.Config, sipClient *sip.Client, ttsEngine *tts.EdgeTTS, sttEngine *stt.VoskSTT, brainClient *brain.Mimo) *Handler {
	return &Handler{
		sipClient: sipClient,
		tts:       ttsEngine,
		stt:       sttEngine,
		brain:     brainClient,
		cfg:       cfg,
	}
}

func (h *Handler) Call(ctx context.Context, targetNumber, fromNumber, greeting string) error {
	rtpPort := 20000 + rand.Intn(5000)

	// Pre-generate greeting
	log.Printf("[call] pre-generating greeting...")
	greetingPCM, _ := h.generatePCM(greeting)
	log.Printf("[call] greeting ready (%d bytes)", len(greetingPCM))

	log.Printf("[call] calling %s from %s", targetNumber, fromNumber)

	resp, err := h.sipClient.Invite(targetNumber, rtpPort, fromNumber)
	if err != nil {
		return fmt.Errorf("INVITE failed: %w", err)
	}

	remote := rtp.ParseSDPRemote(resp.Body)
	if remote == nil {
		return fmt.Errorf("no remote SDP")
	}

	srtp, err := rtp.NewSRTP(rtpPort, remote)
	if err != nil {
		return err
	}
	defer srtp.Close()

	// SIP keepalive goroutine
	go h.sipKeepalive(ctx, targetNumber, fromNumber)

	// === REAL-TIME CONVERSATION LOOP ===
	currentPCM := greetingPCM
	maxTurns := 10

	for turn := 0; turn < maxTurns; turn++ {
		// PLAY greeting/response IMMEDIATELY
		if len(currentPCM) > 0 {
			log.Printf("[call] [turn %d] playing %d bytes", turn, len(currentPCM))
			srtp.SendPCMU(currentPCM)
			currentPCM = nil
		}

		// LISTEN — streaming: play + STT simultaneously, no batch
		log.Printf("[call] [turn %d] streaming...", turn)
		text := h.realtimeListen(ctx, srtp, 4*time.Second)

		if text == "" {
			currentPCM, _ = h.generatePCM("ببخشید، متوجه نشدم.")
			continue
		}

		// BRAIN + TTS in parallel
		log.Printf("[call] [turn %d] heard: %s", turn, text)
		systemPrompt := fmt.Sprintf(`تو نمیفادورا هستی، هوش مصنوعی تلفنی مهربون.
به شماره %s زنگ زدی از طرف %s.
خیلی کوتاه جواب بده.`, targetNumber, fromNumber)

		type brainResult struct {
			text string
			err  error
		}
		ch := make(chan brainResult, 1)
		go func() {
			reply, err := h.brain.Chat(ctx, systemPrompt, []brain.Message{{Role: "user", Content: text}})
			ch <- brainResult{reply, err}
		}()

		res := <-ch
		if res.err != nil {
			currentPCM, _ = h.generatePCM("متأسفم.")
			continue
		}
		log.Printf("[call] [turn %d] brain: %s", turn, res.text)

		// Pre-generate NEXT TTS while we play current
		currentPCM, _ = h.generatePCM(res.text)
	}

	log.Println("[call] ending")
	h.sipClient.Bye(fmt.Sprintf("sip:%s@%s", targetNumber, h.cfg.SIP.Host))
	return nil
}

// realtimeListen — streaming: play each RTP chunk immediately, feed STT
func (h *Handler) realtimeListen(ctx context.Context, srtp *rtp.SRTP, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	lastRecv := time.Now()
	var allPCM []byte

	// Silence keepalive
	silenceDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		silence := make([]byte, 160)
		for {
			select {
			case <-silenceDone:
				return
			case <-ticker.C:
				srtp.SendSilence(silence)
			}
		}
	}()

	// Read loop — each packet: play IMMEDIATELY + collect for STT
	for time.Now().Before(deadline) {
		srtp.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		buf := make([]byte, 1500)
		n, _, err := srtp.ReadFromUDP(buf)
		if err != nil {
			if time.Since(lastRecv) > 2*time.Second && len(allPCM) > 0 {
				break
			}
			continue
		}
		if n < 12 {
			continue
		}

		chunk := make([]byte, n-12)
		copy(chunk, buf[12:n])
		allPCM = append(allPCM, chunk...)
		lastRecv = time.Now()

		// PLAY ON LAPTOP IMMEDIATELY (parallel)
		go h.playChunkLocal(chunk)
	}

	close(silenceDone)

	if len(allPCM) < 800 {
		return ""
	}

	log.Printf("[call] collected %d bytes (%.1fs)", len(allPCM), float64(len(allPCM))/8000)

	// STT
	text, _ := h.stt.Transcribe(allPCM)
	return text
}

func (h *Handler) sipKeepalive(ctx context.Context, targetNumber, fromNumber string) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.sipClient.Options(fmt.Sprintf("sip:%s", h.cfg.SIP.Host))
		}
	}
}

func (h *Handler) generatePCM(text string) ([]byte, error) {
	mp3Data, err := h.tts.Speak(context.Background(), text)
	if err != nil {
		return nil, err
	}
	return mp3ToPCMLaw(mp3Data)
}

func (h *Handler) playChunkLocal(pcmData []byte) {
	// Convert μ-law → WAV → play (afplay needs WAV, not raw μ-law)
	wavFile := fmt.Sprintf("/tmp/nimfadora_c_%d.wav", time.Now().UnixNano())
	defer os.Remove(wavFile)

	cmd := exec.Command("ffmpeg", "-y",
		"-f", "mulaw", "-ar", "8000", "-ac", "1",
		"-i", "pipe:0",
		wavFile)
	cmd.Stdin = bytes.NewReader(pcmData)
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return
	}

	exec.Command("afplay", wavFile).Run()
}

func mp3ToPCMLaw(mp3Data []byte) ([]byte, error) {
	cmd := exec.Command("ffmpeg", "-i", "pipe:0",
		"-f", "mulaw", "-ar", "8000", "-ac", "1", "pipe:1")
	cmd.Stdin = bytes.NewReader(mp3Data)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
