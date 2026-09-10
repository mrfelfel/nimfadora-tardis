package sip

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
	"github.com/nimfadora/tardis/internal/config"
)

type Client struct {
	cfg      config.SIPConfig
	ua       *sipgo.UserAgent
	client   *sipgo.Client
	server   *sipgo.Server
	dialogCC *sipgo.DialogClientCache
	dialog   *sipgo.DialogClientSession
	localIP  string
	rtpPort  int
	byeCh    chan struct{}
	byeOnce  sync.Once
}

type Response struct {
	StatusCode int
	Reason     string
	Body       string
}

func NewClient(cfg config.SIPConfig, transport string) (*Client, error) {
	localIP := getLocalIPMust()

	ua, err := sipgo.NewUA(
		sipgo.WithUserAgent("Nimfadora/0.3.0"),
		sipgo.WithUserAgentHostname(localIP),
	)
	if err != nil {
		return nil, fmt.Errorf("sipgo UA: %w", err)
	}

	cli, err := sipgo.NewClient(ua,
		sipgo.WithClientHostname(localIP),
		sipgo.WithClientPort(cfg.Port),
	)
	if err != nil {
		ua.Close()
		return nil, fmt.Errorf("sipgo client: %w", err)
	}

	srv, err := sipgo.NewServer(ua)
	if err != nil {
		ua.Close()
		return nil, fmt.Errorf("sipgo server: %w", err)
	}

	contactHDR := sip.ContactHeader{
		Address: sip.Uri{User: cfg.Username, Host: localIP, Port: cfg.Port},
	}
	dialogCC := sipgo.NewDialogClientCache(cli, contactHDR)

	c := &Client{
		cfg:      cfg,
		ua:       ua,
		client:   cli,
		server:   srv,
		dialogCC: dialogCC,
		localIP:  localIP,
		byeCh:    make(chan struct{}, 1),
	}

	// Handle incoming BYE
	srv.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
		reasonStr := "none"
		if rHdr := req.GetHeader("Reason"); rHdr != nil {
			reasonStr = rHdr.Value()
		}
		log.Printf("[sipgo] ← BYE received! Reason: %s", reasonStr)
		tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
		c.byeOnce.Do(func() { close(c.byeCh) })
	})

	// Handle incoming re-INVITE — auto-respond 200 OK with SDP
	srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
		log.Println("[sipgo] ← re-INVITE from server")
		sdp := fmt.Sprintf("v=0\r\no=- 0 0 IN IP4 %s\r\ns=Nimfadora\r\nc=IN IP4 %s\r\nt=0 0\r\nm=audio %d RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\na=ptime:20\r\na=sendrecv\r\n",
			c.localIP, c.localIP, c.rtpPort)
		res := sip.NewResponseFromRequest(req, 200, "OK", nil)
		res.SetBody([]byte(sdp))
		res.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
		res.AppendHeader(sip.NewHeader("Session-Expires", "1800;refresher=uas"))
		tx.Respond(res)
		log.Println("[sipgo] → 200 OK to re-INVITE")
	})

	// Handle UPDATE — auto-respond 200 OK
	srv.OnUpdate(func(req *sip.Request, tx sip.ServerTransaction) {
		log.Println("[sipgo] ← UPDATE (session refresh)")
		tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
	})

	// Handle OPTIONS — auto-respond 200 OK
	srv.OnOptions(func(req *sip.Request, tx sip.ServerTransaction) {
		tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
	})

	return c, nil
}

func (c *Client) ByeCh() <-chan struct{} { return c.byeCh }
func (c *Client) Close() error {
	if c.server != nil {
		c.server.Close()
	}
	return c.ua.Close()
}

func (c *Client) Register() error {
	recipient := sip.Uri{Host: c.cfg.Host, Port: c.cfg.Port}
	req := sip.NewRequest(sip.REGISTER, recipient)
	fromAddr := sip.Uri{User: c.cfg.Username, Host: c.cfg.Host}
	fromTag := sip.GenerateTagN(16)
	fromParams := sip.NewParams()
	fromParams.Add("tag", fromTag)
	req.AppendHeader(&sip.FromHeader{Address: fromAddr, Params: fromParams})
	req.AppendHeader(&sip.ToHeader{Address: sip.Uri{User: c.cfg.Username, Host: c.cfg.Host}})
	cid := sip.CallIDHeader(sip.GenerateTagN(20))
	req.AppendHeader(&cid)
	req.AppendHeader(&sip.CSeqHeader{SeqNo: 1, MethodName: sip.REGISTER})
	req.AppendHeader(&sip.ContactHeader{Address: sip.Uri{User: c.cfg.Username, Host: c.localIP, Port: c.cfg.Port}})
	req.AppendHeader(sip.NewHeader("Expires", "3600"))
	req.AppendHeader(sip.NewHeader("User-Agent", "Nimfadora/0.3.0"))

	ctx := context.Background()
	go c.server.ListenAndServe(ctx, "udp", fmt.Sprintf("%s:%d", c.localIP, c.cfg.Port))

	tx, err := c.client.TransactionRequest(ctx, req)
	if err != nil {
		return err
	}
	resp := <-tx.Responses()
	log.Printf("[sipgo] REGISTER → %d %s", resp.StatusCode, resp.Reason)

	if resp.StatusCode == 401 {
		log.Println("[sipgo] REGISTER 401, digest auth...")
		cred, err := c.computeAuth("REGISTER", fmt.Sprintf("sip:%s", c.cfg.Host), resp)
		if err != nil {
			return fmt.Errorf("compute REGISTER auth: %w", err)
		}

		req2 := sip.NewRequest(sip.REGISTER, recipient)
		fromParams2 := sip.NewParams()
		fromParams2.Add("tag", fromTag)
		req2.AppendHeader(&sip.FromHeader{Address: fromAddr, Params: fromParams2})
		req2.AppendHeader(&sip.ToHeader{Address: sip.Uri{User: c.cfg.Username, Host: c.cfg.Host}})
		req2.AppendHeader(&cid)
		req2.AppendHeader(&sip.CSeqHeader{SeqNo: 2, MethodName: sip.REGISTER})
		req2.AppendHeader(&sip.ContactHeader{Address: sip.Uri{User: c.cfg.Username, Host: c.localIP, Port: c.cfg.Port}})
		req2.AppendHeader(sip.NewHeader("Expires", "3600"))
		req2.AppendHeader(sip.NewHeader("Authorization", cred))
		req2.AppendHeader(sip.NewHeader("User-Agent", "Nimfadora/0.3.0"))

		tx2, err := c.client.TransactionRequest(ctx, req2)
		if err != nil {
			return err
		}
		resp = <-tx2.Responses()
		log.Printf("[sipgo] REGISTER → %d %s", resp.StatusCode, resp.Reason)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("REGISTER failed: %d", resp.StatusCode)
	}
	log.Println("[sipgo] registered successfully")
	return nil
}

func (c *Client) Invite(targetNumber string, rtpPort int, fromNumber string) (*Response, error) {
	c.rtpPort = rtpPort
	recipient := sip.Uri{User: targetNumber, Host: c.cfg.Host}
	sdp := []byte(fmt.Sprintf("v=0\r\no=- 0 0 IN IP4 %s\r\ns=Nimfadora\r\nc=IN IP4 %s\r\nt=0 0\r\nm=audio %d RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\na=ptime:20\r\na=sendrecv\r\n",
		c.localIP, c.localIP, rtpPort))

	inviteReq := sip.NewRequest(sip.INVITE, recipient)
	inviteReq.SetBody(sdp)
	inviteReq.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
	inviteReq.AppendHeader(sip.NewHeader("User-Agent", "Nimfadora/0.3.0"))

	// Navaphone OpenSIPS requires From username to match registered auth username
	fromUsername := c.cfg.Username
	fromTag := sip.GenerateTagN(16)
	fromParams := sip.NewParams()
	fromParams.Add("tag", fromTag)
	inviteReq.AppendHeader(&sip.FromHeader{
		Address: sip.Uri{User: fromUsername, Host: c.cfg.Host, Scheme: "sip"},
		Params:  fromParams,
	})
	inviteReq.AppendHeader(&sip.ToHeader{
		Address: sip.Uri{User: targetNumber, Host: c.cfg.Host, Scheme: "sip"},
	})
	cid := sip.CallIDHeader(sip.GenerateTagN(20))
	inviteReq.AppendHeader(&cid)
	inviteReq.AppendHeader(&sip.CSeqHeader{SeqNo: 1, MethodName: sip.INVITE})
	inviteReq.AppendHeader(&sip.ContactHeader{
		Address: sip.Uri{User: fromUsername, Host: c.localIP, Port: c.cfg.Port, Scheme: "sip"},
	})

	ctx := context.Background()
	tx, err := c.client.TransactionRequest(ctx, inviteReq, sipgo.ClientRequestAddVia)
	if err != nil {
		return nil, fmt.Errorf("transaction: %w", err)
	}

	authRetries := 0
	lastReq := inviteReq
	var earlySDP string
	log.Println("[sipgo] waiting for answer...")
	for {
		select {
		case r := <-tx.Responses():
			log.Printf("[sipgo] INVITE → %d %s", r.StatusCode, r.Reason)
			if r.IsSuccess() {
				// Send standard RFC 3261 ACK directly for 200 OK
				ack := c.buildACK(lastReq, r)
				if err := c.client.WriteRequest(ack); err != nil {
					log.Printf("[sipgo] warning: ACK write failed: %v", err)
				} else {
					log.Printf("[sipgo] → ACK sent for 200 OK to %s", ack.Destination())
				}

				// If server retransmits 200 OK (e.g. packet loss or delay), re-send ACK
				tx.OnRetransmission(func(res *sip.Response) {
					if res.IsSuccess() {
						log.Println("[sipgo] ← 200 OK retransmitted by server, re-sending ACK")
						c.client.WriteRequest(ack)
					}
				})

				var remoteSDP string
				if len(r.Body()) > 0 {
					remoteSDP = string(r.Body())
				} else if earlySDP != "" {
					remoteSDP = earlySDP
				}
				return &Response{StatusCode: 200, Reason: "OK", Body: remoteSDP}, nil
			}
			if r.IsProvisional() {
				if r.StatusCode == 183 && len(r.Body()) > 0 && earlySDP == "" {
					earlySDP = string(r.Body())
					log.Printf("[sipgo] captured early SDP from 183 (%d bytes)", len(earlySDP))
				}
				continue
			}
			// Handle 407 Proxy Auth Required or 401 Unauthorized
			if r.StatusCode == 407 || r.StatusCode == 401 {
				authRetries++
				if authRetries > 3 {
					return nil, fmt.Errorf("INVITE auth failed after %d retries", authRetries)
				}
				log.Println("[sipgo] INVITE challenge received, calculating digest auth...")
				tx.Terminate()

				authURI := inviteReq.Recipient.String()
				cred, err := c.computeAuth("INVITE", authURI, r)
				if err != nil {
					return nil, fmt.Errorf("compute INVITE auth: %w", err)
				}

				authReq := sip.NewRequest(sip.INVITE, recipient)
				authReq.SetBody(sdp)
				authReq.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
				authReq.AppendHeader(sip.NewHeader("User-Agent", "Nimfadora/0.3.0"))

				authFromParams := sip.NewParams()
				authFromParams.Add("tag", fromTag)
				authReq.AppendHeader(&sip.FromHeader{
					Address: sip.Uri{User: fromUsername, Host: c.cfg.Host, Scheme: "sip"},
					Params:  authFromParams,
				})
				authReq.AppendHeader(&sip.ToHeader{
					Address: sip.Uri{User: targetNumber, Host: c.cfg.Host, Scheme: "sip"},
				})
				authReq.AppendHeader(&cid)
				authReq.AppendHeader(&sip.CSeqHeader{SeqNo: 2, MethodName: sip.INVITE})
				authReq.AppendHeader(&sip.ContactHeader{
					Address: sip.Uri{User: fromUsername, Host: c.localIP, Port: c.cfg.Port, Scheme: "sip"},
				})

				if r.StatusCode == 407 {
					authReq.AppendHeader(sip.NewHeader("Proxy-Authorization", cred))
				} else {
					authReq.AppendHeader(sip.NewHeader("Authorization", cred))
				}

				tx2, err := c.client.TransactionRequest(ctx, authReq, sipgo.ClientRequestAddVia)
				if err != nil {
					return nil, err
				}
				lastReq = authReq
				tx = tx2
				continue
			}
			return nil, fmt.Errorf("INVITE failed: %d %s", r.StatusCode, r.Reason)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (c *Client) buildACK(inviteReq *sip.Request, resp200 *sip.Response) *sip.Request {
	targetURI := inviteReq.Recipient
	if contact := resp200.Contact(); contact != nil {
		targetURI = contact.Address
	}

	ack := sip.NewRequest(sip.ACK, targetURI)
	if from := inviteReq.From(); from != nil {
		ack.AppendHeader(sip.HeaderClone(from))
	}
	if to := resp200.To(); to != nil {
		ack.AppendHeader(sip.HeaderClone(to))
	}
	if callID := inviteReq.CallID(); callID != nil {
		ack.AppendHeader(sip.HeaderClone(callID))
	}

	cseq := inviteReq.CSeq()
	ack.AppendHeader(&sip.CSeqHeader{
		SeqNo:      cseq.SeqNo,
		MethodName: sip.ACK,
	})

	maxFwd := sip.MaxForwardsHeader(70)
	ack.AppendHeader(&maxFwd)

	// Add Route headers from Record-Route in reverse order (RFC 3261 12.2.1.1)
	recordRoutes := resp200.GetHeaders("Record-Route")
	for i := len(recordRoutes) - 1; i >= 0; i-- {
		ack.AppendHeader(sip.NewHeader("Route", recordRoutes[i].Value()))
	}

	return ack
}

func (c *Client) SessionRefresh() {
	// Keepalive: send OPTIONS every 15 seconds if needed
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for i := 0; i < 20; i++ {
		select {
		case <-c.byeCh:
			return
		case <-ticker.C:
			// No-op or lightweight keepalive
		}
	}
}

func (c *Client) Options(uri string) error {
	recipient := sip.Uri{Host: c.cfg.Host, Port: c.cfg.Port}
	req := sip.NewRequest(sip.OPTIONS, recipient)
	req.AppendHeader(sip.NewHeader("User-Agent", "Nimfadora/0.3.0"))
	tx, err := c.client.TransactionRequest(context.Background(), req)
	if err != nil {
		return err
	}
	<-tx.Responses()
	return nil
}

func (c *Client) Bye(uri string) error {
	if c.dialog != nil {
		return c.dialog.Bye(context.Background())
	}
	return nil
}

func (c *Client) computeAuth(method, uri string, resp *sip.Response) (string, error) {
	h := resp.GetHeader("WWW-Authenticate")
	if h == nil {
		h = resp.GetHeader("Proxy-Authenticate")
	}
	if h == nil {
		return "", fmt.Errorf("no auth challenge header found")
	}

	chal, err := digest.ParseChallenge(h.Value())
	if err != nil {
		return "", fmt.Errorf("parse challenge: %w", err)
	}

	cred, err := digest.Digest(chal, digest.Options{
		Method:   method,
		URI:      uri,
		Username: c.cfg.Username,
		Password: c.cfg.Password,
	})
	if err != nil {
		return "", fmt.Errorf("compute digest: %w", err)
	}

	return cred.String(), nil
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
