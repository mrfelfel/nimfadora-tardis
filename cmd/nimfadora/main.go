package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/nimfadora/tardis/internal/agent"
	"github.com/nimfadora/tardis/internal/api"
	"github.com/nimfadora/tardis/internal/brain"
	"github.com/nimfadora/tardis/internal/call"
	"github.com/nimfadora/tardis/internal/config"
	"github.com/nimfadora/tardis/internal/mcpgate"
	"github.com/nimfadora/tardis/internal/monitor"
	"github.com/nimfadora/tardis/internal/sip"
	"github.com/nimfadora/tardis/internal/stt"
	"github.com/nimfadora/tardis/internal/tts"
)

func main() {
	configPath := flag.String("config", "config.yaml", "config file path")
	target := flag.String("call", "", "destination number (To) for immediate test call")
	fromNumber := flag.String("from", "", "caller number (From)")
	greeting := flag.String("greeting", "Hello, I am Nimfadora TARDIS. I have an important notification for you.", "greeting message")
	transport := flag.String("transport", "udp", "SIP transport: udp or tcp")
	gatewayAddr := flag.String("addr", "0.0.0.0:8080", "MCP Gateway and Agent Web UI address")
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		log.Println("[init] no .env file, reading environment variables")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Printf("[init] warning: config file not loaded (%v), using default settings", err)
		cfg = &config.Config{}
	}

	fmt.Println("  _____ ___  ____  ____ ___ ____   ")
	fmt.Println(" |_   _/ _ \\|  _ \\|  _ \\_ _/ ___|  ")
	fmt.Println("   | || |_| | |_) | | | | |\\___ \\  ")
	fmt.Println("   | ||  _  |  _ <| |_| | | ___) | ")
	fmt.Println("   |_||_| |_|_| \\_\\____/___|____/  ")
	fmt.Println(" Nimfadora TARDIS - MCP Gateway & Autonomous Agent")
	fmt.Println()

	// 1. Initialize Pipeline Metrics
	metrics := monitor.NewMetrics()
	monServer := monitor.NewServer(metrics)
	monServer.Start("0.0.0.0:8199")

	// 2. Initialize Internal Telephony (Optional / Graceful)
	var callHandler *call.Handler
	if cfg.SIP.Username != "" && cfg.SIP.Password != "" {
		ttsEngine := tts.New(cfg.TTS)
		voskModel := "/tmp/vosk/vosk-model-small-fa-0.42"
		voskSTT := stt.NewVoskSTT(voskModel)
		_ = voskSTT.Init()

		sipClient, err := sip.NewClient(cfg.SIP, *transport)
		if err == nil {
			if err := sipClient.Register(); err == nil {
				log.Printf("[telephony] SIP registered → %s:%d (%s)", cfg.SIP.Host, cfg.SIP.Port, *transport)
				var brainClient *brain.Mimo
				if cfg.Brain.APIKey != "" {
					brainClient = brain.New(cfg.Brain)
				}
				callHandler = call.New(cfg, sipClient, ttsEngine, voskSTT, brainClient, metrics)
				apiServer := api.NewServer(metrics)
				apiServer.Start("0.0.0.0:8200")
				callHandler.RegisterAPI(apiServer)
			} else {
				log.Printf("[telephony] SIP registration notice: %v (running in voice-simulated mode)", err)
			}
		} else {
			log.Printf("[telephony] SIP client notice: %v", err)
		}
	} else {
		log.Println("[telephony] SIP credentials not provided. Telephony module running in simulation mode.")
	}

	// 3. Initialize MCP Gateway (ToolPlane Control Plane)
	gateway := mcpgate.New(cfg.Gateway)
	mcpgate.RegisterDefaultTools(gateway, callHandler, cfg.Gateway.CodingBackend, cfg.Gateway.CodingModel)

	// 4. Initialize Brain & Autonomous TARDIS Agent
	var brainClient *brain.Mimo
	if cfg.Brain.APIKey != "" {
		brainClient = brain.New(cfg.Brain)
		log.Printf("[agent] Brain initialized with model: %s", cfg.Brain.Model)
	} else {
		log.Println("[agent] Warning: BRAIN_API_KEY is not set. Brain responses will need an API key.")
	}

	tardisAgent := agent.New(brainClient, gateway)

	// 5. Start Web Control Plane & MCP Server
	server := mcpgate.NewServer(gateway, tardisAgent)
	go func() {
		if err := server.Start(*gatewayAddr); err != nil {
			log.Fatalf("[gateway] server failed: %v", err)
		}
	}()

	log.Printf("[ready] TARDIS Web UI & Agent Chat: http://%s", *gatewayAddr)
	log.Printf("[ready] MCP Gateway Endpoint:       http://%s/mcp", *gatewayAddr)
	log.Printf("[ready] Metrics Dashboard:          http://0.0.0.0:8199")

	// Direct CLI call test if requested
	if *target != "" && callHandler != nil {
		from := *fromNumber
		if from == "" {
			from = cfg.SIP.FromNumber
		}
		go func() {
			log.Printf("[call] Initiating direct test call to %s", *target)
			if err := callHandler.Call(context.Background(), *target, from, *greeting); err != nil {
				log.Printf("[call] direct call error: %v", err)
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("[done] TARDIS shutting down cleanly")
}
