package sip

import (
	"fmt"
	"log"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/icholy/digest"
	"github.com/nimfadora/tardis/internal/config"
)

type Client struct {
	cfg            config.SIPConfig
	conn           *net.UDPConn
	serverIP       string
	localIP        string
	callID         string
	cseq           int
	fromTag        string
	username       string
	realm          string
	nonce          string
	pendingRTPPort int
	sync.RWMutex
}

type Response struct {
	StatusCode int
	Reason     string
	Headers    map[string]string
	Body       string
}

func NewClient(cfg config.SIPConfig, transport string) (*Client, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("resolve SIP server: %w", err)
	}

	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, fmt.Errorf("dial SIP server: %w", err)
	}

	localIP := getLocalIPMust()

	return &Client{
		cfg:      cfg,
		conn:     conn,
		serverIP: udpAddr.IP.String(),
		localIP:  localIP,
		callID:   fmt.Sprintf("%d@nimfadora", time.Now().UnixNano()),
		cseq:     1,
		fromTag:  generateTag(),
		username: cfg.Username,
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) sendAndRead(msg string) (*Response, error) {
	_, err := c.conn.Write([]byte(msg))
	if err != nil {
		return nil, err
	}
	return c.readResponse()
}

func (c *Client) readResponse() (*Response, error) {
	buf := make([]byte, 65535)
	c.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	n, err := c.conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("read SIP: %w", err)
	}
	raw := string(buf[:n])
	log.Printf("[sip] raw %d bytes:\n%s", n, raw[:min(500, len(raw))])
	return parseResponse(raw), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (c *Client) Register() error {
	uri := fmt.Sprintf("sip:%s", c.cfg.Host)
	fromTag := c.fromTag

	msg := fmt.Sprintf("REGISTER %s SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP %s:%d;rport;branch=z9hG4bK%d\r\n"+
		"Max-Forwards: 70\r\n"+
		"From: <sip:%s@%s>;tag=%s\r\n"+
		"To: <sip:%s@%s>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: %d REGISTER\r\n"+
		"Contact: <sip:%s@%s:%d>;expires=3600;rinstance=%s\r\n"+
		"Expires: 3600\r\n"+
		"User-Agent: %s\r\n"+
		"Content-Length: 0\r\n"+
		"\r\n",
		uri,
		c.localIP, c.cfg.Port, rand.Int63(),
		c.username, c.cfg.Host, fromTag,
		c.username, c.cfg.Host,
		c.callID,
		c.cseq,
		c.username, c.localIP, c.cfg.Port,
		c.cfg.UserAgent,
	)
	c.cseq++

	resp, err := c.sendAndRead(msg)
	if err != nil {
		return fmt.Errorf("REGISTER: %w", err)
	}

	if resp.StatusCode == 401 {
		log.Println("[sip] 401, trying digest auth...")
		resp, err = c.sendAuthRequest("REGISTER", uri, resp)
		if err != nil {
			return fmt.Errorf("REGISTER auth: %w", err)
		}
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("REGISTER failed: %d %s", resp.StatusCode, resp.Reason)
	}

	log.Println("[sip] registered")
	return nil
}

func (c *Client) Invite(targetNumber string, rtpPort int, fromNumber string) (*Response, error) {
	c.pendingRTPPort = rtpPort
	uri := fmt.Sprintf("sip:%s@%s", targetNumber, c.cfg.Host)

	sdp := GenerateSDP(c.localIP, rtpPort)

	cseq := c.cseq
	c.cseq++

	msg := fmt.Sprintf("INVITE %s SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP %s:%d;rport;branch=z9hG4bK%d\r\n"+
		"Max-Forwards: 70\r\n"+
		"From: <sip:%s@%s>;tag=%s\r\n"+
		"To: <sip:%s@%s>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: %d INVITE\r\n"+
		"Contact: <sip:%s@%s:%d>\r\n"+
		"Content-Type: application/sdp\r\n"+
		"User-Agent: %s\r\n"+
		"Content-Length: %d\r\n"+
		"\r\n"+
		"%s",
		uri,
		c.localIP, c.cfg.Port, rand.Int63(),
		fromNumber, c.cfg.Host, c.fromTag,
		targetNumber, c.cfg.Host,
		c.callID,
		cseq,
		c.username, c.localIP, c.cfg.Port,
		c.cfg.UserAgent,
		len(sdp),
		sdp,
	)

	resp, err := c.sendAndRead(msg)
	if err != nil {
		return nil, fmt.Errorf("INVITE: %w", err)
	}

	log.Printf("[sip] INVITE response: %d %s", resp.StatusCode, resp.Reason)

	// Wait for provisional responses and handle auth (60s for ringing)
	for resp.StatusCode < 200 || resp.StatusCode == 401 || resp.StatusCode == 407 {
		log.Printf("[sip] response: %d %s", resp.StatusCode, resp.Reason)

		// Handle auth challenges
		if resp.StatusCode == 401 || resp.StatusCode == 407 {
			log.Printf("[sip] %d auth challenge, retrying...", resp.StatusCode)
			c.sendACK(uri, cseq, "")
			resp, err = c.sendAuthRequest("INVITE", uri, resp)
			if err != nil {
				return nil, fmt.Errorf("INVITE auth: %w", err)
			}
			continue
		}

		if resp.StatusCode >= 200 {
			// Extract To tag from response
			toTag := extractTag(resp.Headers["To"])
			c.sendACK(uri, cseq, toTag)
			log.Printf("[sip] ACK sent for %d (toTag=%s)", resp.StatusCode, toTag)
			break
		}

		// Provisional - read next response
		resp, err = c.readResponse()
		if err != nil {
			return nil, fmt.Errorf("wait for answer: %w", err)
		}
	}

	return resp, nil
}

func (c *Client) sendACK(uri string, cseq int, toTag string) {
	toHeader := fmt.Sprintf("<sip:%s@%s>", c.username, c.cfg.Host)
	if toTag != "" {
		toHeader = fmt.Sprintf("<sip:%s@%s>;tag=%s", c.username, c.cfg.Host, toTag)
	}

	ack := fmt.Sprintf("ACK %s SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP %s:%d;rport;branch=z9hG4bK%d\r\n"+
		"Max-Forwards: 70\r\n"+
		"From: <sip:%s@%s>;tag=%s\r\n"+
		"To: %s\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: %d ACK\r\n"+
		"Content-Length: 0\r\n"+
		"\r\n",
		uri,
		c.localIP, c.cfg.Port, rand.Int63(),
		c.username, c.cfg.Host, c.fromTag,
		toHeader,
		c.callID,
		cseq,
	)
	c.conn.Write([]byte(ack))
}

func (c *Client) Options(uri string) error {
	cseq := c.cseq
	c.cseq++

	msg := fmt.Sprintf("OPTIONS %s SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP %s:%d;rport;branch=z9hG4bK%d\r\n"+
		"Max-Forwards: 70\r\n"+
		"From: <sip:%s@%s>;tag=%s\r\n"+
		"To: <sip:%s@%s>\r\n"+
		"Call-ID: keepalive-%d\r\n"+
		"CSeq: %d OPTIONS\r\n"+
		"Content-Length: 0\r\n"+
		"\r\n",
		uri,
		c.localIP, c.cfg.Port, rand.Int63(),
		c.username, c.cfg.Host, c.fromTag,
		c.username, c.cfg.Host,
		time.Now().UnixNano(),
		cseq,
	)
	_, err := c.sendAndRead(msg)
	return err
}

func (c *Client) Bye(uri string) error {
	cseq := c.cseq
	c.cseq++

	msg := fmt.Sprintf("BYE %s SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP %s:%d;rport;branch=z9hG4bK%d\r\n"+
		"Max-Forwards: 70\r\n"+
		"From: \"%s\" <sip:%s@%s>;tag=%s\r\n"+
		"To: <sip:%s@%s>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: %d BYE\r\n"+
		"Content-Length: 0\r\n"+
		"\r\n",
		uri,
		c.localIP, c.cfg.Port, rand.Int63(),
		c.username, c.username, c.cfg.Host, c.fromTag,
		c.username, c.cfg.Host,
		c.callID,
		cseq,
	)

	_, err := c.sendAndRead(msg)
	return err
}

func (c *Client) sendAuthRequest(method, uri string, challenge *Response) (*Response, error) {
	isProxyAuth := challenge.StatusCode == 407
	authHeaderName := "Authorization"

	var challengeStr string
	if isProxyAuth {
		challengeStr = challenge.Headers["Proxy-Authenticate"]
		authHeaderName = "Proxy-Authorization"
	} else {
		challengeStr = challenge.Headers["WWW-Authenticate"]
	}

	chal, err := digest.ParseChallenge(challengeStr)
	if err != nil {
		return nil, fmt.Errorf("parse challenge: %w", err)
	}

	creds, err := digest.Digest(chal, digest.Options{
		Method:   method,
		URI:      uri,
		Username: c.username,
		Password: c.cfg.Password,
	})
	if err != nil {
		return nil, fmt.Errorf("digest: %w", err)
	}

	authHeaderValue := creds.String()

	cseq := c.cseq
	c.cseq++

	// Build SDP body for INVITE re-auth
	sdpBody := ""
	contentType := ""
	if method == "INVITE" && c.pendingRTPPort > 0 {
		sdpBody = GenerateSDP(c.localIP, c.pendingRTPPort)
		contentType = "Content-Type: application/sdp\r\n"
	}

	msg := fmt.Sprintf("%s %s SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP %s:%d;rport;branch=z9hG4bK%d\r\n"+
		"Max-Forwards: 70\r\n"+
		"From: \"%s\" <sip:%s@%s>;tag=%s\r\n"+
		"To: <sip:%s@%s>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: %d %s\r\n"+
		"%s: %s\r\n"+
		"Contact: <sip:%s@%s:%d>\r\n"+
		"%s"+
		"User-Agent: %s\r\n"+
		"Content-Length: %d\r\n"+
		"\r\n"+
		"%s",
		method, uri,
		c.localIP, c.cfg.Port, rand.Int63(),
		c.username, c.username, c.cfg.Host, c.fromTag,
		c.username, c.cfg.Host,
		c.callID,
		cseq, method,
		authHeaderName, authHeaderValue,
		c.username, c.localIP, c.cfg.Port,
		contentType,
		c.cfg.UserAgent,
		len(sdpBody),
		sdpBody,
	)

	return c.sendAndRead(msg)
}

func GenerateSDP(localIP string, rtpPort int) string {
	return fmt.Sprintf("v=0\r\n"+
		"o=- 0 0 IN IP4 %s\r\n"+
		"s=Nimfadora\r\n"+
		"c=IN IP4 %s\r\n"+
		"t=0 0\r\n"+
		"m=audio %d RTP/AVP 0\r\n"+
		"a=rtpmap:0 PCMU/8000\r\n"+
		"a=ptime:20\r\n"+
		"a=sendrecv\r\n",
		localIP, localIP, rtpPort)
}

func parseResponse(raw string) *Response {
	lines := strings.Split(raw, "\r\n")
	if len(lines) == 0 {
		return nil
	}

	resp := &Response{Headers: make(map[string]string)}

	// Find SIP status line
	for _, line := range lines {
		if strings.HasPrefix(line, "SIP/2.0 ") {
			parts := strings.SplitN(line, " ", 3)
			if len(parts) >= 2 {
				resp.StatusCode, _ = strconv.Atoi(parts[1])
				if len(parts) >= 3 {
					resp.Reason = parts[2]
				}
			}
			break
		}
	}

	inBody := false
	var bodyLines []string
	for _, line := range lines[1:] {
		if inBody {
			bodyLines = append(bodyLines, line)
			continue
		}
		if line == "" {
			inBody = true
			continue
		}
		if idx := strings.Index(line, ":"); idx > 0 {
			resp.Headers[strings.TrimSpace(line[:idx])] = strings.TrimSpace(line[idx+1:])
		}
	}

	resp.Body = strings.Join(bodyLines, "\r\n")
	return resp
}

func extractParam(header, param string) string {
	idx := strings.Index(header, param+"=\"")
	if idx < 0 {
		return ""
	}
	rest := header[idx+len(param)+2:]
	end := strings.Index(rest, "\"")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func generateTag() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
}

func extractTag(header string) string {
	idx := strings.Index(header, "tag=")
	if idx < 0 {
		return ""
	}
	rest := header[idx+4:]
	end := strings.IndexAny(rest, ";,> ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func getLocalIPMust() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return ipNet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}
