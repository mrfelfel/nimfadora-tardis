package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	SIP     SIPConfig     `yaml:"sip"`
	Media   MediaConfig   `yaml:"media"`
	TTS     TTSConfig     `yaml:"tts"`
	Brain   BrainConfig   `yaml:"brain"`
	Agent   AgentConfig   `yaml:"agent"`
	Gateway GatewayConfig `yaml:"gateway"`
}

type GatewayConfig struct {
	Addr            string          `yaml:"addr"`
	AllowDangerous  bool            `yaml:"allow_dangerous"`
	RequireApproval []string        `yaml:"require_approval"`
	AllowedTools    []string        `yaml:"allowed_tools"`
	DeniedTools     []string        `yaml:"denied_tools"`
	CodingBackend   string          `yaml:"coding_backend"` // "opencode", "claude", "builtin"
	CodingModel     string          `yaml:"coding_model"`   // e.g. "glm-4", "claude-3-5-sonnet", etc.
}

type SIPConfig struct {
	Host       string `yaml:"host"`
	Port       int    `yaml:"port"`
	Username   string `yaml:"username"`
	FromNumber string `yaml:"from_number"`
	Password   string `yaml:"-"`
	Transport  string `yaml:"transport"`
	Expires    int    `yaml:"expires"`
	UserAgent  string `yaml:"user_agent"`
}

type MediaConfig struct {
	RTPPortStart int    `yaml:"rtp_port_start"`
	RTPPortEnd   int    `yaml:"rtp_port_end"`
	Codec        string `yaml:"codec"`
	SampleRate   int    `yaml:"sample_rate"`
}

type TTSConfig struct {
	Voice  string `yaml:"voice"`
	Rate   string `yaml:"rate"`
	Volume string `yaml:"volume"`
}

type BrainConfig struct {
	APIKey      string  `yaml:"-"`
	BaseURL     string  `yaml:"base_url"`
	Model       string  `yaml:"model"`
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float64 `yaml:"temperature"`
}

type AgentConfig struct {
	Script   string `yaml:"script"`
	LogLevel string `yaml:"log_level"`
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
	if dom := os.Getenv("SIP_DOMAIN"); dom != "" {
		cfg.SIP.Host = dom
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
		cfg.Gateway.RequireApproval = []string{
			"telephony_make_call",
			"system_run_dangerous_command",
			"git_force_push",
			"db_drop_table",
		}
	}

	return cfg, nil
}
