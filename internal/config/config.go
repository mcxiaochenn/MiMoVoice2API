// Package config 从环境变量加载并校验运行配置。
// 配置只在启动时解析一次，之后以结构体注入各组件。
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
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
	// defaultMaxConcurrentRequests 是并发处理上限的默认值。
	// ASR 上传走全量缓冲，单请求峰值内存约为上传上限的 4~5 倍
	// （multipart 缓冲 + 原始字节 + base64 + JSON），4 × 25 MiB × 4 ≈ 400 MiB
	// 封顶，适配 1 GiB 级自托管主机；内存充裕或上传很小时可调高。
	defaultMaxConcurrentRequests = 4
)

// Config 是服务的完整运行配置。
type Config struct {
	MIMOAPIKey            string
	MIMOBaseURL           string
	MIMOASRModel          string
	MIMOTTSModel          string
	ListenAddr            string
	AuthToken             string
	MaxUploadBytes        int64
	MaxTTSChars           int
	MaxConcurrentRequests int
	UpstreamTimeout       time.Duration
	LogLevel              slog.Level
	LogFormat             string
	MetricsEnabled        bool
}

// Load 读取进程环境变量。
func Load() (Config, error) {
	return load(os.Getenv)
}

// load 允许测试注入环境变量读取函数。
func load(getenv func(string) string) (Config, error) {
	cfg := Config{
		MIMOAPIKey:            strings.TrimSpace(getenv("MIMO_API_KEY")),
		MIMOBaseURL:           strings.TrimRight(stringOrDefault(getenv("MIMO_BASE_URL"), defaultBaseURL), "/"),
		MIMOASRModel:          stringOrDefault(getenv("MIMO_ASR_MODEL"), defaultASRModel),
		MIMOTTSModel:          stringOrDefault(getenv("MIMO_TTS_MODEL"), defaultTTSModel),
		ListenAddr:            stringOrDefault(getenv("LISTEN_ADDR"), defaultListenAddr),
		AuthToken:             strings.TrimSpace(getenv("AUTH_TOKEN")),
		MaxUploadBytes:        defaultMaxUploadBytes,
		MaxTTSChars:           defaultMaxTTSChars,
		MaxConcurrentRequests: defaultMaxConcurrentRequests,
		UpstreamTimeout:       defaultUpstreamTimeout,
		LogFormat:             strings.ToLower(stringOrDefault(getenv("LOG_FORMAT"), "json")),
		MetricsEnabled:        true,
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

	if raw := strings.TrimSpace(getenv("MAX_CONCURRENT_REQUESTS")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return Config{}, fmt.Errorf("MAX_CONCURRENT_REQUESTS must be a positive integer, got %q", raw)
		}
		cfg.MaxConcurrentRequests = v
	}

	if raw := strings.TrimSpace(getenv("METRICS_ENABLED")); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("METRICS_ENABLED must be true or false, got %q", raw)
		}
		cfg.MetricsEnabled = v
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

	// 无鉴权时只允许回环监听：默认配置（:8080 绑定所有接口）会把未鉴权服务
	// 直接暴露到网络里，必须在启动阶段就拒绝，而不是只打一行警告。
	if cfg.AuthToken == "" {
		if err := validateLoopbackListen(cfg.ListenAddr); err != nil {
			return Config{}, err
		}
	}

	return cfg, nil
}

// validateLoopbackListen 校验 LISTEN_ADDR 绑定的是回环地址。
func validateLoopbackListen(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("LISTEN_ADDR %q must be in host:port form", addr)
	}
	if hostIsLoopback(host) {
		return nil
	}
	return fmt.Errorf(
		"AUTH_TOKEN is empty but LISTEN_ADDR %q is not a loopback address; set AUTH_TOKEN or listen on a loopback address such as 127.0.0.1:8080",
		addr,
	)
}

// hostIsLoopback 判断监听主机是否为回环。
// 空主机（":8080"）与 0.0.0.0 / :: 一样表示绑定所有接口，不算回环；
// 用 netip 解析而不是字符串前缀，IPv4、IPv6（含 zone）与 localhost 都覆盖。
func hostIsLoopback(host string) bool {
	host = strings.TrimSpace(host)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
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
