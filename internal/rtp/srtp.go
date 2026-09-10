package rtp

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"
)

type SRTP struct {
	conn          *net.UDPConn
	remote        *net.UDPAddr
	ssrc          uint32
	seq           uint16
	ts            uint32
	mu            sync.Mutex
	silencePaused bool
}

func NewSRTP(localPort int, remote *RemoteInfo) (*SRTP, error) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("0.0.0.0:%d", localPort))
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen RTP: %w", err)
	}
	remoteAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", remote.IP, remote.Port))
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &SRTP{
		conn:   conn,
		remote: remoteAddr,
		ssrc:   rand.Uint32(),
	}, nil
}

func (s *SRTP) Close() error          { return s.conn.Close() }
func (s *SRTP) LocalAddr() net.Addr   { return s.conn.LocalAddr() }
func (s *SRTP) ReadFromUDP(buf []byte) (int, *net.UDPAddr, error) {
	return s.conn.ReadFromUDP(buf)
}
func (s *SRTP) SetReadDeadline(t time.Time) { s.conn.SetReadDeadline(t) }
func (s *SRTP) MySSRC() uint32             { return s.ssrc }

func (s *SRTP) SetSilencePaused(pause bool) {
	s.mu.Lock()
	s.silencePaused = pause
	s.mu.Unlock()
}

func (s *SRTP) SendSilence(payload []byte) {
	s.mu.Lock()
	if s.silencePaused {
		s.mu.Unlock()
		return
	}
	defer s.mu.Unlock()
	pkt := s.buildPacket(payload)
	s.conn.WriteToUDP(pkt, s.remote)
	s.seq++
	s.ts += uint32(len(payload))
}

// SendPCMU sends μ-law PCM data as RTP packets with drift-free exact 20ms timing
func (s *SRTP) SendPCMU(pcmData []byte) error {
	samplesPerPacket := 160
	packetDuration := 20 * time.Millisecond
	startTime := time.Now()
	packetIndex := 0

	for offset := 0; offset < len(pcmData); offset += samplesPerPacket {
		end := offset + samplesPerPacket
		if end > len(pcmData) {
			end = len(pcmData)
		}
		payload := pcmData[offset:end]

		s.mu.Lock()
		pkt := s.buildPacket(payload)
		s.seq++
		s.ts += uint32(len(payload))
		s.mu.Unlock()

		if _, err := s.conn.WriteToUDP(pkt, s.remote); err != nil {
			return fmt.Errorf("send RTP: %w", err)
		}

		packetIndex++
		nextTarget := startTime.Add(time.Duration(packetIndex) * packetDuration)
		sleepDuration := time.Until(nextTarget)
		if sleepDuration > 0 {
			time.Sleep(sleepDuration)
		}
	}
	return nil
}

// SendPCMUStream reads chunks from channel and sends as RTP packets with exact 20ms pacing.
// First packet is sent immediately; subsequent packets are paced.
func (s *SRTP) SendPCMUStream(ch <-chan []byte) error {
	samplesPerPacket := 160
	packetDuration := 20 * time.Millisecond
	startTime := time.Now()
	packetIndex := 0

	for chunk := range ch {
		// Pad partial chunks to 160 bytes (required for μ-law at 20ms)
		if len(chunk) < samplesPerPacket {
			padded := make([]byte, samplesPerPacket)
			copy(padded, chunk)
			chunk = padded
		}

		s.mu.Lock()
		pkt := s.buildPacket(chunk)
		s.seq++
		s.ts += uint32(len(chunk))
		s.mu.Unlock()

		if _, err := s.conn.WriteToUDP(pkt, s.remote); err != nil {
			return fmt.Errorf("send RTP stream: %w", err)
		}

		packetIndex++
		nextTarget := startTime.Add(time.Duration(packetIndex) * packetDuration)
		sleepDuration := time.Until(nextTarget)
		if sleepDuration > 0 {
			time.Sleep(sleepDuration)
		}
	}
	return nil
}

func (s *SRTP) buildPacket(payload []byte) []byte {
	pkt := make([]byte, 12+len(payload))
	pkt[0] = 0x80
	pkt[1] = 0x00
	binary.BigEndian.PutUint16(pkt[2:4], s.seq)
	binary.BigEndian.PutUint32(pkt[4:8], s.ts)
	binary.BigEndian.PutUint32(pkt[8:12], s.ssrc)
	copy(pkt[12:], payload)
	return pkt
}
