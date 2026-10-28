package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func loadFrom(t *testing.T, env map[string]string) (Config, error) {
	t.Helper()
	return load(func(key string) string { return env[key] })
}

func TestLoadDefaults(t *testing.T) {
	// 默认 LISTEN_ADDR 是 :8080（绑定所有接口），必须配 AUTH_TOKEN 才合法，
	// 因此这里带上 token 来验证默认值；无鉴权场景见 TestLoadListenGuard。
	cfg, err := loadFrom(t, map[string]string{"MIMO_API_KEY": "test-key", "AUTH_TOKEN": "secret"})
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}

	if cfg.MIMOBaseURL != defaultBaseURL {
		t.Errorf("MIMOBaseURL = %q, want %q", cfg.MIMOBaseURL, defaultBaseURL)
	}
	if cfg.MIMOASRModel != "mimo-v2.5-asr" {
		t.Errorf("MIMOASRModel = %q", cfg.MIMOASRModel)
	}
	if cfg.MIMOTTSModel != "mimo-v2.5-tts" {
		t.Errorf("MIMOTTSModel = %q", cfg.MIMOTTSModel)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.MaxUploadBytes != 25<<20 {
		t.Errorf("MaxUploadBytes = %d", cfg.MaxUploadBytes)
	}
	if cfg.MaxTTSChars != 1000 {
		t.Errorf("MaxTTSChars = %d", cfg.MaxTTSChars)
	}
	if cfg.MaxConcurrentRequests != 4 {
		t.Errorf("MaxConcurrentRequests = %d, want 4", cfg.MaxConcurrentRequests)
	}
	if cfg.UpstreamTimeout != 180*time.Second {
		t.Errorf("UpstreamTimeout = %v", cfg.UpstreamTimeout)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q", cfg.LogFormat)
	}
	if !cfg.MetricsEnabled {
		t.Error("MetricsEnabled = false, want true by default")
	}
}

// 无鉴权时只允许回环监听；配了 AUTH_TOKEN 则不限制。
func TestLoadListenGuard(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{name: "默认监听无鉴权被拒绝", env: map[string]string{"MIMO_API_KEY": "k"}, wantErr: true},
		{name: "空主机绑定所有接口", env: map[string]string{"MIMO_API_KEY": "k", "LISTEN_ADDR": ":8080"}, wantErr: true},
		{name: "IPv4 通配地址", env: map[string]string{"MIMO_API_KEY": "k", "LISTEN_ADDR": "0.0.0.0:8080"}, wantErr: true},
		{name: "IPv6 通配地址", env: map[string]string{"MIMO_API_KEY": "k", "LISTEN_ADDR": "[::]:8080"}, wantErr: true},
		{name: "局域网地址", env: map[string]string{"MIMO_API_KEY": "k", "LISTEN_ADDR": "192.168.1.10:8080"}, wantErr: true},
		{name: "公网主机名", env: map[string]string{"MIMO_API_KEY": "k", "LISTEN_ADDR": "example.com:8080"}, wantErr: true},
		{name: "缺少端口", env: map[string]string{"MIMO_API_KEY": "k", "LISTEN_ADDR": "127.0.0.1"}, wantErr: true},
		{name: "IPv4 回环", env: map[string]string{"MIMO_API_KEY": "k", "LISTEN_ADDR": "127.0.0.1:8080"}},
		{name: "IPv4 回环段", env: map[string]string{"MIMO_API_KEY": "k", "LISTEN_ADDR": "127.0.0.53:8080"}},
		{name: "IPv6 回环", env: map[string]string{"MIMO_API_KEY": "k", "LISTEN_ADDR": "[::1]:8080"}},
		{name: "localhost", env: map[string]string{"MIMO_API_KEY": "k", "LISTEN_ADDR": "localhost:8080"}},
		{name: "localhost 大小写", env: map[string]string{"MIMO_API_KEY": "k", "LISTEN_ADDR": "LOCALHOST:8080"}},
		{name: "有鉴权时允许通配监听", env: map[string]string{"MIMO_API_KEY": "k", "AUTH_TOKEN": "secret", "LISTEN_ADDR": ":8080"}},
		{name: "有鉴权时允许公网地址", env: map[string]string{"MIMO_API_KEY": "k", "AUTH_TOKEN": "secret", "LISTEN_ADDR": "0.0.0.0:8080"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadFrom(t, tt.env)
			if tt.wantErr {
				if err == nil {
					t.Fatal("load() error = nil, want error")
				}
				if !strings.Contains(err.Error(), "loopback") && !strings.Contains(err.Error(), "host:port") {
					t.Fatalf("error = %q, want loopback / host:port hint", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("load() error = %v", err)
			}
			if cfg.AuthToken != strings.TrimSpace(tt.env["AUTH_TOKEN"]) {
				t.Errorf("AuthToken = %q", cfg.AuthToken)
			}
		})
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := loadFrom(t, map[string]string{
		"MIMO_API_KEY":            "  key  ",
		"MIMO_BASE_URL":           "http://127.0.0.1:9000/v1/",
		"MIMO_ASR_MODEL":          "custom-asr",
		"MIMO_TTS_MODEL":          "custom-tts",
		"LISTEN_ADDR":             "127.0.0.1:8088",
		"AUTH_TOKEN":              "secret",
		"MAX_UPLOAD_BYTES":        "1048576",
		"MAX_TTS_CHARS":           "500",
		"MAX_CONCURRENT_REQUESTS": "16",
		"UPSTREAM_TIMEOUT":        "30s",
		"LOG_LEVEL":               "DEBUG",
		"LOG_FORMAT":              "text",
		"METRICS_ENABLED":         "false",
	})
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}

	if cfg.MIMOAPIKey != "key" {
		t.Errorf("MIMOAPIKey = %q", cfg.MIMOAPIKey)
	}
	if cfg.MIMOBaseURL != "http://127.0.0.1:9000/v1" {
		t.Errorf("MIMOBaseURL = %q", cfg.MIMOBaseURL)
	}
	if cfg.MIMOASRModel != "custom-asr" || cfg.MIMOTTSModel != "custom-tts" {
		t.Errorf("models = %q / %q", cfg.MIMOASRModel, cfg.MIMOTTSModel)
	}
	if cfg.ListenAddr != "127.0.0.1:8088" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.AuthToken != "secret" {
		t.Errorf("AuthToken = %q", cfg.AuthToken)
	}
	if cfg.MaxUploadBytes != 1048576 {
		t.Errorf("MaxUploadBytes = %d", cfg.MaxUploadBytes)
	}
	if cfg.MaxTTSChars != 500 {
		t.Errorf("MaxTTSChars = %d", cfg.MaxTTSChars)
	}
	if cfg.MaxConcurrentRequests != 16 {
		t.Errorf("MaxConcurrentRequests = %d, want 16", cfg.MaxConcurrentRequests)
	}
	if cfg.UpstreamTimeout != 30*time.Second {
		t.Errorf("UpstreamTimeout = %v", cfg.UpstreamTimeout)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v", cfg.LogLevel)
	}
	if cfg.LogFormat != "text" {
		t.Errorf("LogFormat = %q", cfg.LogFormat)
	}
	if cfg.MetricsEnabled {
		t.Error("MetricsEnabled = true, want false")
	}
}

func TestValidateBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{name: "官方地址", raw: "https://api.xiaomimimo.com/v1"},
		{name: "本地测试桩", raw: "http://127.0.0.1:9000/v1"},
		{name: "代理带路径前缀", raw: "https://proxy.example.com/mimo/v1"},
		{name: "尾部斜杠", raw: "https://api.xiaomimimo.com/v1/"},
		{name: "缺少版本段", raw: "https://api.example.com", wantErr: "/v1"},
		{name: "版本段不是最后一段", raw: "https://api.example.com/v1/chat", wantErr: "/v1"},
		{name: "版本号不对", raw: "https://api.example.com/v2", wantErr: "/v1"},
		{name: "前缀吞掉斜杠", raw: "https://api.example.com/xv1", wantErr: "/v1"},
		{name: "带 query", raw: "https://api.example.com/v1?key=1", wantErr: "query"},
		{name: "带空 query", raw: "https://api.example.com/v1?", wantErr: "query"},
		{name: "带 fragment", raw: "https://api.example.com/v1#frag", wantErr: "fragment"},
		{name: "带 userinfo", raw: "https://user:pass@api.example.com/v1", wantErr: "userinfo"},
		{name: "协议错误", raw: "ftp://api.example.com/v1", wantErr: "http or https"},
		{name: "缺少 host", raw: "https:///v1", wantErr: "host"},
		{name: "非法 URL", raw: "http://[::1/v1", wantErr: "valid URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBaseURL(tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateBaseURL(%q) error = %v", tt.raw, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateBaseURL(%q) error = nil, want %q", tt.raw, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantSub string
	}{
		{
			name:    "missing api key",
			env:     map[string]string{},
			wantSub: "MIMO_API_KEY",
		},
		{
			name:    "invalid base url scheme",
			env:     map[string]string{"MIMO_API_KEY": "k", "MIMO_BASE_URL": "ftp://example.com/v1"},
			wantSub: "http or https",
		},
		{
			name:    "base url without host",
			env:     map[string]string{"MIMO_API_KEY": "k", "MIMO_BASE_URL": "https:///v1"},
			wantSub: "host",
		},
		{
			name:    "non numeric upload limit",
			env:     map[string]string{"MIMO_API_KEY": "k", "MAX_UPLOAD_BYTES": "big"},
			wantSub: "MAX_UPLOAD_BYTES",
		},
		{
			name:    "zero upload limit",
			env:     map[string]string{"MIMO_API_KEY": "k", "MAX_UPLOAD_BYTES": "0"},
			wantSub: "MAX_UPLOAD_BYTES",
		},
		{
			name:    "non numeric concurrency limit",
			env:     map[string]string{"MIMO_API_KEY": "k", "MAX_CONCURRENT_REQUESTS": "many"},
			wantSub: "MAX_CONCURRENT_REQUESTS",
		},
		{
			name:    "zero concurrency limit",
			env:     map[string]string{"MIMO_API_KEY": "k", "MAX_CONCURRENT_REQUESTS": "0"},
			wantSub: "MAX_CONCURRENT_REQUESTS",
		},
		{
			name:    "bad duration",
			env:     map[string]string{"MIMO_API_KEY": "k", "UPSTREAM_TIMEOUT": "2 minutes"},
			wantSub: "UPSTREAM_TIMEOUT",
		},
		{
			name:    "bad log level",
			env:     map[string]string{"MIMO_API_KEY": "k", "LOG_LEVEL": "verbose"},
			wantSub: "LOG_LEVEL",
		},
		{
			name:    "bad log format",
			env:     map[string]string{"MIMO_API_KEY": "k", "LOG_FORMAT": "xml"},
			wantSub: "LOG_FORMAT",
		},
		{
			name:    "bad metrics flag",
			env:     map[string]string{"MIMO_API_KEY": "k", "METRICS_ENABLED": "yes-please"},
			wantSub: "METRICS_ENABLED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadFrom(t, tt.env)
			if err == nil {
				t.Fatal("load() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantSub)
			}
		})
	}
}

// 配置错误会写入启动日志，不能带出地址中的凭据或查询参数。
func TestBaseURLErrorDoesNotExposeSecrets(t *testing.T) {
	for _, raw := range []string{
		"https://user:private-password@api.example.com/v1",
		"https://api.example.com/v1?token=private-token",
		"https://api.example.com/v1#private-fragment",
		"https://user:private-password@api.example.com/%zz",
	} {
		err := validateBaseURL(raw)
		if err == nil {
			t.Fatalf("expected invalid URL")
		}
		if strings.Contains(err.Error(), "private-") {
			t.Fatalf("error exposes credentials: %s", err)
		}
	}
}
