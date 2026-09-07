package rtp

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"time"
)

type Receiver struct {
	conn *net.UDPConn
}

func NewReceiver(localPort int) (*Receiver, error) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("0.0.0.0:%d", localPort))
	if err != nil {
		return nil, err
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen RTP recv: %w", err)
	}

	return &Receiver{conn: conn}, nil
}

func (r *Receiver) LocalAddr() net.Addr {
	return r.conn.LocalAddr()
}

func (r *Receiver) Close() error {
	return r.conn.Close()
}

// CollectPCMU listens for RTP packets for the given duration and returns raw μ-law PCM
func (r *Receiver) CollectPCMU(duration time.Duration) ([]byte, error) {
	var pcm []byte
	deadline := time.Now().Add(duration)

	for time.Now().Before(deadline) {
		r.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		buf := make([]byte, 1500)
		n, _, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		if n < 12 {
			continue
		}
		payload := buf[12:n]
		pcm = append(pcm, payload...)
	}

	log.Printf("[rtp-recv] collected %d bytes PCM", len(pcm))
	return pcm, nil
}

func (r *Receiver) ReadLoop(onPacket func(payload []byte, seq uint16, ts uint32)) {
	buf := make([]byte, 1500)
	for {
		r.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, _, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n < 12 {
			continue
		}
		seq := binary.BigEndian.Uint16(buf[2:4])
		ts := binary.BigEndian.Uint32(buf[4:8])
		if onPacket != nil {
			onPacket(buf[12:n], seq, ts)
		}
	}
}
