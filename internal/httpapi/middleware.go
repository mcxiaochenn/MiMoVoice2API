package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

type contextKey int

const (
	requestIDKey contextKey = iota
	requestInfoKey
)

type requestInfo struct {
	route          string
	modelRequested string
	modelUpstream  string
	upstreamStatus int
	inBytes        int64
	errCode        string
}

func requestInfoFrom(ctx context.Context) *requestInfo {
	info, _ := ctx.Value(requestInfoKey).(*requestInfo)
	return info
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func (s *server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get("X-Request-ID"))
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func (s *server) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		info := &requestInfo{route: routeName(r.URL.Path)}
		ctx := context.WithValue(r.Context(), requestInfoKey, info)
		rec := &responseRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r.WithContext(ctx))

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		s.log.LogAttrs(ctx, slog.LevelInfo, "request",
			slog.String("request_id", requestIDFrom(ctx)),
			slog.String("route", info.route),
			slog.Int("status", status),
			slog.Int64("dur_ms", time.Since(start).Milliseconds()),
			slog.Int64("in_bytes", info.inBytes),
			slog.Int64("out_bytes", rec.bytes),
			slog.String("model_requested", info.modelRequested),
			slog.String("model_upstream", info.modelUpstream),
			slog.Int("upstream_status", info.upstreamStatus),
			slog.String("err_code", info.errCode),
		)
	})
}

func (s *server) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.log.Error("panic recovered",
					"request_id", requestIDFrom(r.Context()),
					"panic", fmt.Sprint(recovered),
					"stack", string(debug.Stack()),
				)
				s.writeAPIError(w, r, newAPIError("internal_error", "", "internal server error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AuthToken == "" || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			s.writeAPIError(w, r, newAPIError("unauthorized", "", "missing bearer token"))
			return
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AuthToken)) != 1 {
			s.writeAPIError(w, r, newAPIError("invalid_token", "", "bearer token does not match"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// responseRecorder 记录状态码与写出字节数，并把 Flush 透传给底层 Writer。
type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *responseRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", false
	}
	return parts[1], true
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req-unknown"
	}
	return hex.EncodeToString(b[:])
}

func sanitizeRequestID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 64 {
		return ""
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
		default:
			return ""
		}
	}
	return id
}

func routeName(path string) string {
	switch path {
	case "/v1/audio/transcriptions":
		return "transcriptions"
	case "/v1/models":
		return "models"
	case "/healthz":
		return "healthz"
	default:
		return "unknown"
	}
}
