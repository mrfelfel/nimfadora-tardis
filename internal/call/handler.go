package call

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math/rand"
	"os/exec"
	"strings"
	"time"

	"github.com/nimfadora/tardis/internal/api"
	"github.com/nimfadora/tardis/internal/audio"
	"github.com/nimfadora/tardis/internal/brain"
	"github.com/nimfadora/tardis/internal/config"
	"github.com/nimfadora/tardis/internal/monitor"
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
	player    *audio.Player
	metrics   *monitor.PipelineMetrics
	turnCount int32
}

func New(cfg *config.Config, sipClient *sip.Client, ttsEngine *tts.EdgeTTS, sttEngine *stt.VoskSTT, brainClient *brain.Mimo, metrics *monitor.PipelineMetrics) *Handler {
	return &Handler{
		sipClient: sipClient,
		tts:       ttsEngine,
		stt:       sttEngine,
		brain:     brainClient,
		cfg:       cfg,
		player:    audio.NewPlayer(),
		metrics:   metrics,
	}
}

func (h *Handler) RegisterAPI(apiServer *api.Server) {
	apiServer.SetCallHandler(func(ctx context.Context, req api.CallRequest) error {
		from := req.From
		if from == "" {
			if h.cfg.SIP.FromNumber != "" {
				from = h.cfg.SIP.FromNumber
			} else {
				from = h.cfg.SIP.Username
			}
		}
		greeting := req.Greeting
		if greeting == "" {
			greeting = "Hello, I am Nimfadora TARDIS. How can I assist you today?"
		}
		return h.Call(ctx, req.Target, from, greeting)
	})

	apiServer.SetNotifyHandler(func(ctx context.Context, req api.NotifyRequest) error {
		from := req.From
		if from == "" {
			if h.cfg.SIP.FromNumber != "" {
				from = h.cfg.SIP.FromNumber
			} else {
				from = h.cfg.SIP.Username
			}
		}
		return h.Notify(ctx, req.Target, from, req.Message)
	})
}

func (h *Handler) Call(ctx context.Context, targetNumber, fromNumber, greeting string) error {
	rtpPort := 20000 + rand.Intn(5000)

	log.Printf("[call] pre-generating greeting...")
	greetingStart := time.Now()
	greetingPCM, err := h.generatePCM(greeting)
	if err != nil {
		return fmt.Errorf("greeting TTS failed: %w", err)
	}
	h.metrics.Emit("greeting_tts", float64(time.Since(greetingStart).Milliseconds()), "ms")
	log.Printf("[call] greeting ready (%d bytes, %.1fs)", len(greetingPCM), time.Since(greetingStart).Seconds())

	log.Printf("[call] calling %s from %s", targetNumber, fromNumber)
	resp, err := h.sipClient.Invite(targetNumber, rtpPort, fromNumber)
	if err != nil {
		return fmt.Errorf("INVITE failed: %w", err)
	}

	remote := rtp.ParseSDPRemote(resp.Body)
	if remote == nil {
		return fmt.Errorf("no remote SDP")
	}

	srtpConn, err := rtp.NewSRTP(rtpPort, remote)
	if err != nil {
		return err
	}
	defer srtpConn.Close()

	// Start session refresh (UPDATE method via sipgo)
	go h.sipClient.SessionRefresh()

	// === CONVERSATION LOOP (EXPERIMENTAL) ===
	// NOTE: Bidirectional voice conversation is currently experimental due to RTP stream
	// synchronization and Vosk/Whisper latency on incoming audio.
	// Production/stable usage uses Notify() / telephony_make_call tool for AI-driven phone alerts.
	// TODO(community/contributors): Complete bidirectional voice turn-taking & barge-in.
	maxTurns := 10
	listenTimeout := 5 * time.Second

	// Play greeting: stream directly to RTP (no pre-buffer)
	if len(greetingPCM) > 0 {
		log.Printf("[call] [greeting] streaming %d bytes (%.1fs)...", len(greetingPCM), float64(len(greetingPCM))/8000)
		go h.player.PlayULaw(greetingPCM)
		if err := srtpConn.SendPCMU(greetingPCM); err != nil {
			return fmt.Errorf("greeting RTP: %w", err)
		}
	}

	for turn := 0; turn < maxTurns; turn++ {
		byeCh := h.sipClient.ByeCh()
		select {
		case <-byeCh:
			log.Println("[call] BYE — ending")
			return nil
		case <-ctx.Done():
			return nil
		default:
		}

		h.metrics.EmitTurnStart(turn)
		turnStart := time.Now()

		// LISTEN for caller speech
		log.Printf("[call] [turn %d] listening...", turn)
		text := h.realtimeListen(ctx, srtpConn, listenTimeout)

		if text == "" {
			log.Printf("[call] [turn %d] no speech", turn)
			srtpConn.SetSilencePaused(false)
			ttsCh, ttsDone := h.tts.StreamSpeakChan(ctx, "I am sorry, I did not catch that.")
			srtpConn.SetSilencePaused(true)
			if err := srtpConn.SendPCMUStream(ttsCh); err != nil {
				log.Printf("[call] [turn %d] RTP error: %v", turn, err)
			}
			<-ttsDone
			srtpConn.SetSilencePaused(false)
			continue
		}

		log.Printf("[call] [turn %d] heard: %s", turn, text)
		systemPrompt := fmt.Sprintf(`You are Nimfadora TARDIS, a friendly AI phone agent.
You called %s on behalf of %s.
Keep answers concise.`, targetNumber, fromNumber)

		// === TRUE STREAMING PIPELINE: Brain stream → Sentence split → TTS → RTP ===
		brainTokens, brainDone := h.brain.ChatStream(ctx, systemPrompt, []brain.Message{{Role: "user", Content: text}})

		// Sentence splitter: reads brain tokens, detects sentence boundaries, sends to TTS → RTP
		sentencesDone := make(chan struct{})
		go func() {
			defer close(sentencesDone)
			var buf string
			for token := range brainTokens {
				buf += token
				// Split on sentence boundaries (Farsi + English)
				for {
					idx := -1
					for _, sep := range []string{".\n", "\n", ". ", "! ", "? ", "", "! ", "", "", "!"} {
						if i := indexOf(buf, sep); i >= 0 {
							if idx < 0 || i < idx {
								idx = i + len(sep)
							}
						}
					}
					if idx < 0 {
						break
					}
					sentence := buf[:idx]
					buf = buf[idx:]
					sentence = trimSpace(sentence)
					if sentence == "" {
						continue
					}
					log.Printf("[call] [turn %d] → tts: %s", turn, sentence)
					ttsCh, ttsDone := h.tts.StreamSpeakChan(ctx, sentence)
					srtpConn.SetSilencePaused(true)
					if err := srtpConn.SendPCMUStream(ttsCh); err != nil {
						log.Printf("[call] [turn %d] RTP error: %v", turn, err)
					}
					<-ttsDone
					srtpConn.SetSilencePaused(false)
				}
			}
			// Flush remaining buffer
			buf = trimSpace(buf)
			if buf != "" {
				log.Printf("[call] [turn %d] → tts: %s", turn, buf)
				ttsCh, ttsDone := h.tts.StreamSpeakChan(ctx, buf)
				srtpConn.SetSilencePaused(true)
				srtpConn.SendPCMUStream(ttsCh)
				<-ttsDone
				srtpConn.SetSilencePaused(false)
			}
		}()

		// Wait for brain to finish, then wait for all sentences to be spoken
		<-brainDone
		<-sentencesDone

		h.metrics.EmitTotalLatency(float64(time.Since(turnStart).Milliseconds()))
	}

	log.Println("[call] ending")
	h.sipClient.Bye(fmt.Sprintf("sip:%s@%s", targetNumber, h.cfg.SIP.Host))
	h.player.Close()
	return nil
}

// Notify makes a one-way notification call: call → play message → hang up
func (h *Handler) Notify(ctx context.Context, targetNumber, fromNumber, message string) error {
	rtpPort := 20000 + rand.Intn(5000)

	log.Printf("[notify] calling %s: %s", targetNumber, message)
	resp, err := h.sipClient.Invite(targetNumber, rtpPort, fromNumber)
	if err != nil {
		return fmt.Errorf("INVITE failed: %w", err)
	}

	remote := rtp.ParseSDPRemote(resp.Body)
	if remote == nil {
		return fmt.Errorf("no remote SDP")
	}

	srtpConn, err := rtp.NewSRTP(rtpPort, remote)
	if err != nil {
		return err
	}
	defer srtpConn.Close()

	// Monitor context cancellation — send BYE immediately if stopped
	go func() {
		<-ctx.Done()
		log.Printf("[notify] context cancelled, sending BYE")
		h.sipClient.Bye(fmt.Sprintf("sip:%s@%s", targetNumber, h.cfg.SIP.Host))
		srtpConn.Close()
	}()

	// Stream TTS message → RTP
	log.Printf("[notify] streaming TTS...")
	ttsCh, ttsDone := h.tts.StreamSpeakChan(ctx, message)
	if err := srtpConn.SendPCMUStream(ttsCh); err != nil {
		log.Printf("[notify] RTP error: %v", err)
	}
	<-ttsDone

	// Wait a moment for audio to reach the other end
	time.Sleep(500 * time.Millisecond)

	// Hang up
	log.Printf("[notify] BYE")
	h.sipClient.Bye(fmt.Sprintf("sip:%s@%s", targetNumber, h.cfg.SIP.Host))
	log.Printf("[notify] done")
	return nil
}

func (h *Handler) realtimeListen(ctx context.Context, srtpConn *rtp.SRTP, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	silenceStart := time.Now()
	allPCM := make([]byte, 0, 32000)
	hasReceived := false

	silenceDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		silence := make([]byte, 160)
		for i := range silence {
			silence[i] = 0x7E
		}
		for {
			select {
			case <-silenceDone:
				return
			case <-ticker.C:
				srtpConn.SendSilence(silence)
			}
		}
	}()

	buf := make([]byte, 1500)
	for time.Now().Before(deadline) {
		byeCh := h.sipClient.ByeCh()
		select {
		case <-byeCh:
			close(silenceDone)
			return ""
		default:
		}
		srtpConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, _, err := srtpConn.ReadFromUDP(buf)
		if err != nil {
			// Break early on 2s silence after speech started
			if hasReceived && time.Since(silenceStart) > 2*time.Second {
				break
			}
			continue
		}
		if n < 12 {
			continue
		}
		if !hasReceived {
			hasReceived = true
		}
		silenceStart = time.Now()
		allPCM = append(allPCM, buf[12:n]...)
		h.metrics.EmitRTPPacket(n - 12)
	}
	close(silenceDone)

	if len(allPCM) < 800 {
		return ""
	}
	log.Printf("[call] collected %d bytes (%.1fs)", len(allPCM), float64(len(allPCM))/8000)

	sttStart := time.Now()
	text, _ := h.stt.Transcribe(allPCM)
	h.metrics.EmitSTTDuration(float64(time.Since(sttStart).Milliseconds()))
	return text
}

func (h *Handler) generatePCM(text string) ([]byte, error) {
	mp3Data, err := h.tts.Speak(context.Background(), text)
	if err != nil {
		return nil, err
	}
	return mp3ToPCMLaw(mp3Data)
}

func mp3ToPCMLaw(mp3Data []byte) ([]byte, error) {
	cmd := exec.CommandContext(context.Background(), "ffmpeg", "-y", "-i", "pipe:0",
		"-ar", "8000", "-ac", "1",
		"-af", "aresample=resampler=soxr:precision=20,volume=1.3",
		"-f", "mulaw", "pipe:1")
	cmd.Stdin = bytes.NewReader(mp3Data)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("mp3→μ-law: %w", err)
	}
	return out.Bytes(), nil
}

func indexOf(s, substr string) int {
	return strings.Index(s, substr)
}

func trimSpace(s string) string {
	return strings.TrimSpace(s)
}
