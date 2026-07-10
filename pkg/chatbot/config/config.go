// Package config loads and validates the chatbot configuration from a JSON file.
//
// 설정은 ./conf/config.json 에서 읽으며, 시크릿(텔레그램 토큰, AI 자격증명)은
// 환경변수로 override 할 수 있다.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Config is the top-level configuration loaded from conf/config.json.
type Config struct {
	Telegram TelegramConfig `json:"telegram"`
	AI       AIConfig       `json:"ai"`
}

// TelegramConfig holds Telegram bot settings.
type TelegramConfig struct {
	// Token is the bot token issued by @BotFather.
	Token string `json:"token"`
	// 향후 접근제어용: 허용된 텔레그램 사용자 ID 목록 (M1 미사용).
	AllowedUserIDs []int64 `json:"allowed_user_ids"`
	// ThinkingAnimation is an optional GIF (URL / local path / file_id) shown
	// while the AI is "thinking". GIFs are converted to MP4 by Telegram, so a
	// transparent background is filled with a solid color.
	ThinkingAnimation string `json:"thinking_animation"`
	// ThinkingSticker is an optional sticker (file_id / URL / local .webm/.tgs/.webp
	// path) shown while "thinking". Stickers keep transparency and render small,
	// and take precedence over ThinkingAnimation. When both are empty, a text
	// placeholder ("🤔 생각 중...") is used.
	ThinkingSticker string `json:"thinking_sticker"`
}

// AIConfig describes how to reach the wrapped AI agent API. The harness and tool
// execution run inside the AI server, so the bot only sends a prompt and gets the
// answer back. These values populate the POST body to Endpoint (see internal/ai).
type AIConfig struct {
	// Endpoint is the agent run URL, e.g. "http://127.0.0.1:8443/internal/api/agent/run".
	Endpoint string `json:"endpoint"`
	// Engine selects the inference engine, e.g. "llama".
	Engine string `json:"engine"`
	// Preset names a server-side inference preset. The backend applies all the
	// inference options (temperature, etc.) AND the system prompt bound to it,
	// so the bot only sends the preset name.
	Preset string `json:"preset"`
	// NodeID selects the inference node; sent as nodes:["<node_id>"] in the body.
	// The backend interprets it (e.g. "this", "default", or a specific node id).
	NodeID string `json:"node_id"`
	// MaxTurns caps the agent's reasoning turns.
	MaxTurns int `json:"max_turns"`
	// Credential is the access credential: BASE64(signed message + EOA signature).
	// Auth is not wired yet — this is loaded but not sent.
	Credential string `json:"credential"`
	// Timeout is the per-request timeout as a Go duration string, e.g. "30s".
	Timeout string `json:"timeout"`
}

// TimeoutDuration parses Timeout, falling back to 30s when empty or invalid.
func (a AIConfig) TimeoutDuration() time.Duration {
	if a.Timeout == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(a.Timeout)
	if err != nil {
		return 30 * time.Second
	}
	return d
}

// Load reads, parses, applies environment overrides to, and validates the
// configuration at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	cfg.applyEnvOverrides()

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyEnvOverrides lets secrets be supplied via environment variables so they
// never need to be committed to disk. An env var only overrides when set.
func (c *Config) applyEnvOverrides() {
	if v, ok := os.LookupEnv("TELEGRAM_BOT_TOKEN"); ok {
		c.Telegram.Token = v
	}
	if v, ok := os.LookupEnv("AI_CREDENTIAL"); ok {
		c.AI.Credential = v
	}
	if v, ok := os.LookupEnv("AI_ENDPOINT"); ok {
		c.AI.Endpoint = v
	}
	if v, ok := os.LookupEnv("AI_NODE_ID"); ok {
		c.AI.NodeID = v
	}
}

// validate checks that the values required for the current milestone are present.
func (c *Config) validate() error {
	if c.Telegram.Token == "" {
		return fmt.Errorf("telegram.token is required (set it in config or via TELEGRAM_BOT_TOKEN)")
	}
	return nil
}
