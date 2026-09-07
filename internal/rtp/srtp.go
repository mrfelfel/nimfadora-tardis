package rtp

import (
	"encoding/binary"
	"fmt"
	"log"
	"math/rand"
	"net"
	"time"
)

type SRTP struct {
	conn   *net.UDPConn
	remote *net.UDPAddr
	ssrc   uint32
	seq    uint16
	ts     uint32
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

func (s *SRTP) Close() error {
	return s.conn.Close()
}

func (s *SRTP) LocalAddr() net.Addr {
	return s.conn.LocalAddr()
}

func (s *SRTP) ReadFromUDP(buf []byte) (int, *net.UDPAddr, error) {
	return s.conn.ReadFromUDP(buf)
}

func (s *SRTP) SetReadDeadline(t time.Time) {
	s.conn.SetReadDeadline(t)
}

func (s *SRTP) MySSRC() uint32 {
	return s.ssrc
}

func (s *SRTP) SendSilence(payload []byte) {
	pkt := s.buildPacket(payload)
	s.conn.WriteToUDP(pkt, s.remote)
	s.seq++
	s.ts += uint32(len(payload))
}

func (s *SRTP) SendPCMU(pcmData []byte) error {
	samplesPerPacket := 160
	i := 0

	for i < len(pcmData) {
		end := i + samplesPerPacket
		if end > len(pcmData) {
			end = len(pcmData)
		}
		payload := pcmData[i:end]

		pkt := s.buildPacket(payload)
		_, err := s.conn.WriteToUDP(pkt, s.remote)
		if err != nil {
			return fmt.Errorf("send RTP: %w", err)
		}

		s.seq++
		s.ts += uint32(len(payload))
		i = end
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

func (s *SRTP) CollectPCMU(duration time.Duration) ([]byte, error) {
	var pcm []byte
	deadline := time.Now().Add(duration)
	lastRecv := time.Now()

	// RTP keepalive — send very quiet audio (not pure silence) to prevent server timeout
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		// Very quiet tone (0x7E = near-silence in μ-law)
		keepalive := make([]byte, 160)
		for i := range keepalive {
			keepalive[i] = 0x7E
		}
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				pkt := s.buildPacket(keepalive)
				s.conn.WriteToUDP(pkt, s.remote)
				s.seq++
				s.ts += 160
			}
		}
	}()

	for time.Now().Before(deadline) {
		s.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		buf := make([]byte, 1500)
		n, remoteAddr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if time.Since(lastRecv) > 1*time.Second && len(pcm) > 0 {
				break
			}
			continue
		}
		if n < 12 {
			continue
		}
		// Skip our own echo (server might reflect our packets)
		if remoteAddr != nil && remoteAddr.Port == s.remote.Port && remoteAddr.IP.Equal(s.remote.IP) {
			// This is from the server - legitimate
		}
		pcm = append(pcm, buf[12:n]...)
		lastRecv = time.Now()
	}

	close(done)
	log.Printf("[rtp] collected %d bytes PCM (%.1fs)", len(pcm), float64(len(pcm))/8000)
	return pcm, nil
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
