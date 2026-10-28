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
	cfg, err := loadFrom(t, map[string]string{"MIMO_API_KEY": "test-key"})
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
	if cfg.UpstreamTimeout != 180*time.Second {
		t.Errorf("UpstreamTimeout = %v", cfg.UpstreamTimeout)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q", cfg.LogFormat)
	}
	if cfg.AuthToken != "" {
		t.Errorf("AuthToken = %q, want empty", cfg.AuthToken)
	}
	if !cfg.MetricsEnabled {
		t.Error("MetricsEnabled = false, want true by default")
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := loadFrom(t, map[string]string{
		"MIMO_API_KEY":     "  key  ",
		"MIMO_BASE_URL":    "http://127.0.0.1:9000/v1/",
		"MIMO_ASR_MODEL":   "custom-asr",
		"MIMO_TTS_MODEL":   "custom-tts",
		"LISTEN_ADDR":      "127.0.0.1:8088",
		"AUTH_TOKEN":       "secret",
		"MAX_UPLOAD_BYTES": "1048576",
		"MAX_TTS_CHARS":    "500",
		"UPSTREAM_TIMEOUT": "30s",
		"LOG_LEVEL":        "DEBUG",
		"LOG_FORMAT":       "text",
		"METRICS_ENABLED":  "false",
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
