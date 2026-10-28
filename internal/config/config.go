// Package config 从环境变量加载并校验运行配置。
// 配置只在启动时解析一次，之后以结构体注入各组件。
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL         = "https://api.xiaomimimo.com/v1"
	defaultASRModel        = "mimo-v2.5-asr"
	defaultTTSModel        = "mimo-v2.5-tts"
	defaultListenAddr      = ":8080"
	defaultMaxUploadBytes  = int64(25 << 20) // 25 MiB
	defaultMaxTTSChars     = 1000
	defaultUpstreamTimeout = 180 * time.Second
)

// Config 是服务的完整运行配置。
type Config struct {
	MIMOAPIKey      string
	MIMOBaseURL     string
	MIMOASRModel    string
	MIMOTTSModel    string
	ListenAddr      string
	AuthToken       string
	MaxUploadBytes  int64
	MaxTTSChars     int
	UpstreamTimeout time.Duration
	LogLevel        slog.Level
	LogFormat       string
}

// Load 读取进程环境变量。
func Load() (Config, error) {
	return load(os.Getenv)
}

// load 允许测试注入环境变量读取函数。
func load(getenv func(string) string) (Config, error) {
	cfg := Config{
		MIMOAPIKey:      strings.TrimSpace(getenv("MIMO_API_KEY")),
		MIMOBaseURL:     strings.TrimRight(stringOrDefault(getenv("MIMO_BASE_URL"), defaultBaseURL), "/"),
		MIMOASRModel:    stringOrDefault(getenv("MIMO_ASR_MODEL"), defaultASRModel),
		MIMOTTSModel:    stringOrDefault(getenv("MIMO_TTS_MODEL"), defaultTTSModel),
		ListenAddr:      stringOrDefault(getenv("LISTEN_ADDR"), defaultListenAddr),
		AuthToken:       strings.TrimSpace(getenv("AUTH_TOKEN")),
		MaxUploadBytes:  defaultMaxUploadBytes,
		MaxTTSChars:     defaultMaxTTSChars,
		UpstreamTimeout: defaultUpstreamTimeout,
		LogFormat:       strings.ToLower(stringOrDefault(getenv("LOG_FORMAT"), "json")),
	}

	if cfg.MIMOAPIKey == "" {
		return Config{}, fmt.Errorf("MIMO_API_KEY is required")
	}

	if err := validateBaseURL(cfg.MIMOBaseURL); err != nil {
		return Config{}, err
	}

	if raw := strings.TrimSpace(getenv("MAX_UPLOAD_BYTES")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 {
			return Config{}, fmt.Errorf("MAX_UPLOAD_BYTES must be a positive integer, got %q", raw)
		}
		cfg.MaxUploadBytes = v
	}

	if raw := strings.TrimSpace(getenv("MAX_TTS_CHARS")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return Config{}, fmt.Errorf("MAX_TTS_CHARS must be a positive integer, got %q", raw)
		}
		cfg.MaxTTSChars = v
	}

	if raw := strings.TrimSpace(getenv("UPSTREAM_TIMEOUT")); raw != "" {
		v, err := time.ParseDuration(raw)
		if err != nil || v <= 0 {
			return Config{}, fmt.Errorf("UPSTREAM_TIMEOUT must be a positive duration such as 180s, got %q", raw)
		}
		cfg.UpstreamTimeout = v
	}

	level := strings.ToLower(stringOrDefault(getenv("LOG_LEVEL"), "info"))
	switch level {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = slog.LevelInfo
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		return Config{}, fmt.Errorf("LOG_LEVEL must be one of debug, info, warn, error, got %q", level)
	}

	switch cfg.LogFormat {
	case "json", "text":
	default:
		return Config{}, fmt.Errorf("LOG_FORMAT must be json or text, got %q", cfg.LogFormat)
	}

	if cfg.ListenAddr == "" {
		return Config{}, fmt.Errorf("LISTEN_ADDR must not be empty")
	}

	return cfg, nil
}

func stringOrDefault(raw, fallback string) string {
	if v := strings.TrimSpace(raw); v != "" {
		return v
	}
	return fallback
}

func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("MIMO_BASE_URL is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("MIMO_BASE_URL must use http or https, got %q", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("MIMO_BASE_URL must include a host, got %q", raw)
	}
	return nil
}
