package rtp

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"
)

type Sender struct {
	conn   *net.UDPConn
	remote *net.UDPAddr
	ssrc   uint32
	seq    uint16
laufTS  uint32
}

type RemoteInfo struct {
	IP   string
	Port int
}

func ParseSDPRemote(sdpBody string) *RemoteInfo {
	info := &RemoteInfo{}
	for _, line := range strings.Split(sdpBody, "\r\n") {
		if strings.HasPrefix(line, "c=IN IP4 ") {
			info.IP = strings.TrimPrefix(line, "c=IN IP4 ")
			info.IP = strings.Split(info.IP, " ")[0]
		}
		if strings.HasPrefix(line, "m=audio ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				fmt.Sscanf(parts[1], "%d", &info.Port)
			}
		}
	}
	if info.IP == "" || info.Port == 0 {
		return nil
	}
	return info
}

func NewSender(localPort int, remote *RemoteInfo) (*Sender, error) {
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

	return &Sender{
		conn:   conn,
		remote: remoteAddr,
		ssrc:   rand.Uint32(),
	}, nil
}

func (s *Sender) Close() error {
	return s.conn.Close()
}

func (s *Sender) LocalAddr() net.Addr {
	return s.conn.LocalAddr()
}

func (s *Sender) SendPCMU(pcmData []byte) error {
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
		s.laufTS += uint32(len(payload))
		i = end
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

func (s *Sender) buildPacket(payload []byte) []byte {
	pkt := make([]byte, 12+len(payload))
	pkt[0] = 0x80
	pkt[1] = 0x00
	binary.BigEndian.PutUint16(pkt[2:4], s.seq)
	binary.BigEndian.PutUint32(pkt[4:8], s.laufTS)
	binary.BigEndian.PutUint32(pkt[8:12], s.ssrc)
	copy(pkt[12:], payload)
	return pkt
}
