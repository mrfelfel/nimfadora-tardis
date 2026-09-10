package config

import (
	"log"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// Legacy types kept as aliases so existing packages (brain, sip, tts, call) compile unchanged
type (
	SIPConfig = SIPSettings
	TTSConfig = TTS
	MediaConfig = struct {
		RTPPortStart int    `yaml:"rtp_port_start"`
		RTPPortEnd   int    `yaml:"rtp_port_end"`
		Codec        string `yaml:"codec"`
		SampleRate   int    `yaml:"sample_rate"`
	}
	AgentConfig = struct {
		Script   string `yaml:"script"`
		LogLevel string `yaml:"log_level"`
	}
)

// Config is the legacy top-level config struct (still loaded from YAML at boot)
type Config struct {
	SIP     SIPConfig     `yaml:"sip"`
	Media   MediaConfig   `yaml:"media"`
	TTS     TTSConfig     `yaml:"tts"`
	Brain   BrainConfig   `yaml:"brain"`
	Agent   AgentConfig   `yaml:"agent"`
	Gateway GatewayConfig `yaml:"gateway"`
}

type BrainConfig struct {
	APIKey      string  `yaml:"-"`
	BaseURL     string  `yaml:"base_url"`
	Model       string  `yaml:"model"`
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float64 `yaml:"temperature"`
}

type GatewayConfig struct {
	Addr            string   `yaml:"addr"`
	AllowDangerous  bool     `yaml:"allow_dangerous"`
	RequireApproval []string `yaml:"require_approval"`
	AllowedTools    []string `yaml:"allowed_tools"`
	DeniedTools     []string `yaml:"denied_tools"`
	CodingBackend   string   `yaml:"coding_backend"`
	CodingModel     string   `yaml:"coding_model"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if key := os.Getenv("BRAIN_API_KEY"); key != "" {
		cfg.Brain.APIKey = key
	}
	if pw := os.Getenv("SIP_PASSWORD"); pw != "" {
		cfg.SIP.Password = pw
	}
	if cfg.Gateway.Addr == "" {
		cfg.Gateway.Addr = ":8080"
	}
	if cfg.Gateway.CodingBackend == "" {
		cfg.Gateway.CodingBackend = "opencode"
	}
	if cfg.Gateway.CodingModel == "" {
		cfg.Gateway.CodingModel = "glm-4"
	}
	if len(cfg.Gateway.RequireApproval) == 0 {
		cfg.Gateway.RequireApproval = []string{"telephony_make_call", "system_run_command"}
	}
	return cfg, nil
}

// Settings is the complete runtime configuration exposed via the Web UI
type Settings struct {
	Brain   BrainSettings   `json:"brain" yaml:"brain"`
	Gateway GatewaySettings `json:"gateway" yaml:"gateway"`
	SIP     SIPSettings     `json:"sip" yaml:"sip"`
	TTS     TTS             `json:"tts" yaml:"tts"`
}

type BrainSettings struct {
	BaseURL     string  `json:"base_url" yaml:"base_url"`
	APIKey      string  `json:"api_key" yaml:"api_key"`
	Model       string  `json:"model" yaml:"model"`
	MaxTokens   int     `json:"max_tokens" yaml:"max_tokens"`
	Temperature float64 `json:"temperature" yaml:"temperature"`
}

type GatewaySettings struct {
	AllowDangerous  bool     `json:"allow_dangerous" yaml:"allow_dangerous"`
	RequireApproval []string `json:"require_approval" yaml:"require_approval"`
	AllowedTools    []string `json:"allowed_tools" yaml:"allowed_tools"`
	DeniedTools     []string `json:"denied_tools" yaml:"denied_tools"`
	CodingBackend   string   `json:"coding_backend" yaml:"coding_backend"`
	CodingModel     string   `json:"coding_model" yaml:"coding_model"`
	ResearchBackend string   `json:"research_backend" yaml:"research_backend"`
}

type SIPSettings struct {
	Host       string `json:"host" yaml:"host"`
	Port       int    `json:"port" yaml:"port"`
	Username   string `json:"username" yaml:"username"`
	Password   string `json:"password" yaml:"password"`
	FromNumber string `json:"from_number" yaml:"from_number"`
	Transport  string `json:"transport" yaml:"transport"`
}

type TTS struct {
	Voice  string `json:"voice" yaml:"voice"`
	Rate   string `json:"rate" yaml:"rate"`
	Volume string `json:"volume" yaml:"volume"`
}

// SettingsStore manages live configuration with thread-safe read/write and YAML persistence
type SettingsStore struct {
	mu   sync.RWMutex
	data Settings
	path string
}

var defaultSettings = Settings{
	Brain: BrainSettings{
		BaseURL:     "https://api.openai.com/v1",
		APIKey:      "",
		Model:       "gpt-4o",
		MaxTokens:   256,
		Temperature: 0.7,
	},
	Gateway: GatewaySettings{
		AllowDangerous:  false,
		RequireApproval: []string{"telephony_make_call", "system_run_command", "git_force_push"},
		AllowedTools:    []string{},
		DeniedTools:     []string{},
		CodingBackend:   "opencode",
		CodingModel:     "glm-4",
		ResearchBackend: "builtin",
	},
	SIP: SIPSettings{
		Host:       "voice.navaphone.com",
		Port:       5060,
		Username:   "",
		Password:   "",
		FromNumber: "",
		Transport:  "udp",
	},
	TTS: TTS{
		Voice:  "en-US-GuyNeural",
		Rate:   "+0%",
		Volume: "+0%",
	},
}

func NewSettingsStore(path string) *SettingsStore {
	s := &SettingsStore{
		data: defaultSettings,
		path: path,
	}
	s.loadFromDisk()
	return s
}

func (s *SettingsStore) loadFromDisk() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	_ = yaml.Unmarshal(data, &s.data)
}

func (s *SettingsStore) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

func (s *SettingsStore) Set(newSettings Settings) error {
	s.mu.Lock()
	s.data = newSettings
	err := s.saveToDisk()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	log.Printf("[config] settings saved to %s", s.path)
	return nil
}

func (s *SettingsStore) saveToDisk() error {
	data, err := yaml.Marshal(s.data)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}
