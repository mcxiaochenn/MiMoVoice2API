// Package httpapi 提供 OpenAI 兼容的 HTTP 传输层。
// 只负责解析、校验、错误封装与响应写出，字段语义映射在 adapter 中完成。
package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/config"
	"github.com/mcxiaochenn/MiMoVoice2API/internal/mimo"
)

// BuildInfo 是编译期注入的版本信息。
type BuildInfo struct {
	Version string
	Commit  string
}

type server struct {
	cfg   config.Config
	log   *slog.Logger
	mimo  *mimo.Client
	build BuildInfo
}

// New 组装路由与中间件，返回可直接交给 http.Server 的 Handler。
func New(cfg config.Config, log *slog.Logger, client *mimo.Client, build BuildInfo) http.Handler {
	s := &server{cfg: cfg, log: log, mimo: client, build: build}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/audio/transcriptions", s.method(http.MethodPost, s.handleTranscriptions))
	mux.HandleFunc("/v1/audio/speech", s.method(http.MethodPost, s.handleSpeech))
	mux.HandleFunc("/v1/models", s.method(http.MethodGet, s.handleModels))
	mux.HandleFunc("/healthz", s.method(http.MethodGet, s.handleHealth))
	mux.HandleFunc("/", s.handleNotFound)

	// 顺序：request id → 访问日志 → panic 恢复 → 鉴权 → 路由。
	// recover 必须在访问日志内层，否则 panic 展开时日志拿不到最终状态码。
	return s.withRequestID(s.withAccessLog(s.withRecover(s.withAuth(mux))))
}

func (s *server) method(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			s.writeAPIError(w, r, newAPIError(
				"method_not_allowed", "",
				fmt.Sprintf("method %s is not allowed; use %s", r.Method, method),
			))
			return
		}
		next(w, r)
	}
}

func (s *server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.writeAPIError(w, r, newAPIError("not_found", "", "unknown endpoint: "+r.URL.Path))
}
