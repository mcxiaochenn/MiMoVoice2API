package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/config"
	"github.com/mcxiaochenn/MiMoVoice2API/internal/httpapi"
	"github.com/mcxiaochenn/MiMoVoice2API/internal/mimo"
)

// 由 -ldflags "-X main.version=... -X main.commit=..." 注入。
var (
	version = "dev"
	commit  = "none"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg, os.Stdout)
	if cfg.AuthToken == "" {
		// config 已强制此时只监听回环地址，这里提醒的只剩「无鉴权」这一事实。
		logger.Warn("AUTH_TOKEN is empty; the API is unauthenticated and bound to loopback only")
	}

	client := mimo.New(cfg.MIMOBaseURL, cfg.MIMOAPIKey, cfg.UpstreamTimeout)
	handler := httpapi.New(cfg, logger, client, httpapi.BuildInfo{Version: version, Commit: commit})

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		// 标准库的协议级错误也走结构化日志，保持字段风格一致。
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		// WriteTimeout 故意为 0：M3 的流式响应不能被整体写超时截断。
	}

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddr, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(listener)
	}()

	logger.Info("server started",
		"addr", cfg.ListenAddr,
		"version", version,
		"commit", commit,
		"auth_enabled", cfg.AuthToken != "",
		"metrics_enabled", cfg.MetricsEnabled,
		"upstream", cfg.MIMOBaseURL,
		"max_upload_bytes", cfg.MaxUploadBytes,
	)

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("shutting down", "timeout", "10s")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	}
}

func newLogger(cfg config.Config, w *os.File) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	var handler slog.Handler
	if cfg.LogFormat == "text" {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}
	return slog.New(handler)
}
