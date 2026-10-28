package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/config"
	"github.com/mcxiaochenn/MiMoVoice2API/internal/mimo"
)

var testWAV = []byte("RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00")

func testConfig(upstreamURL string) config.Config {
	return config.Config{
		MIMOAPIKey:      "test-key",
		MIMOBaseURL:     upstreamURL,
		MIMOASRModel:    "mimo-v2.5-asr",
		MIMOTTSModel:    "mimo-v2.5-tts",
		ListenAddr:      ":0",
		AuthToken:       "secret",
		MaxUploadBytes:  1 << 20,
		MaxTTSChars:     1000,
		UpstreamTimeout: 5 * time.Second,
		LogLevel:        slog.LevelError,
		LogFormat:       "json",
	}
}

func testHandler(cfg config.Config) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := mimo.New(cfg.MIMOBaseURL, cfg.MIMOAPIKey, cfg.UpstreamTimeout)
	return New(cfg, logger, client, BuildInfo{Version: "test", Commit: "abc123"})
}

type transcriptionRequest struct {
	fields   map[string]string
	file     []byte
	omitFile bool
	omitAuth bool
	token    string
}

// requestCapture 保护跨 goroutine 共享的请求体，保证 -race 下干净。
type requestCapture struct {
	mu   sync.Mutex
	body map[string]any
}

func (c *requestCapture) store(body map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.body = body
}

func (c *requestCapture) load() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body
}

func doTranscription(t *testing.T, handler http.Handler, spec transcriptionRequest) *httptest.ResponseRecorder {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for key, value := range spec.fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field %q: %v", key, err)
		}
	}
	if !spec.omitFile {
		part, err := writer.CreateFormFile("file", "sample.wav")
		if err != nil {
			t.Fatalf("create file part: %v", err)
		}
		if _, err := part.Write(spec.file); err != nil {
			t.Fatalf("write file part: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if !spec.omitAuth {
		token := spec.token
		if token == "" {
			token = "secret"
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestTranscriptionsSuccess(t *testing.T) {
	var capture requestCapture
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("upstream Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("upstream Content-Type = %q", got)
		}
		var decoded map[string]any
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Errorf("decode upstream request: %v", err)
		} else {
			capture.store(decoded)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id": "chatcmpl-1",
			"model": "mimo-v2.5-asr",
			"choices": [{"index": 0, "finish_reason": "stop", "message": {"role": "assistant", "content": "你好世界"}}],
			"usage": {"seconds": 7, "prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13}
		}`)
	}))
	defer upstream.Close()

	handler := testHandler(testConfig(upstream.URL))
	rec := doTranscription(t, handler, transcriptionRequest{
		fields: map[string]string{
			"model":           "whisper-1",
			"language":        "zh-CN",
			"response_format": "json",
		},
		file: testWAV,
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body transcriptionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Text != "你好世界" {
		t.Errorf("text = %q", body.Text)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID must be set")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}

	captured := capture.load()
	if captured["model"] != "mimo-v2.5-asr" {
		t.Errorf("upstream model = %v", captured["model"])
	}
	if captured["stream"] != false {
		t.Errorf("upstream stream = %v", captured["stream"])
	}
	asrOptions, ok := captured["asr_options"].(map[string]any)
	if !ok || asrOptions["language"] != "zh" {
		t.Errorf("asr_options = %v", captured["asr_options"])
	}

	message := captured["messages"].([]any)[0].(map[string]any)
	part := message["content"].([]any)[0].(map[string]any)
	if part["type"] != "input_audio" {
		t.Errorf("content type = %v", part["type"])
	}
	inputAudio := part["input_audio"].(map[string]any)
	if data, _ := inputAudio["data"].(string); !strings.HasPrefix(data, "data:audio/wav;base64,") {
		t.Errorf("data URL = %q", data)
	}
	if _, exists := inputAudio["format"]; exists {
		t.Error("format must not be sent alongside the data URL")
	}
}

func TestTranscriptionsVerboseJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"model": "mimo-v2.5-asr",
			"choices": [{"finish_reason": "stop", "message": {"content": "hello"}}],
			"usage": {"seconds": 7}
		}`)
	}))
	defer upstream.Close()

	handler := testHandler(testConfig(upstream.URL))
	rec := doTranscription(t, handler, transcriptionRequest{
		fields: map[string]string{
			"model":           "mimo-v2.5-asr",
			"language":        "en-US",
			"response_format": "verbose_json",
		},
		file: testWAV,
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body transcriptionVerboseResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Task != "transcribe" || body.Language != "en" || body.Duration != 7 || body.Text != "hello" {
		t.Errorf("body = %+v", body)
	}
}

func TestTranscriptionsPlainText(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices": [{"finish_reason": "stop", "message": {"content": "纯文本"}}]}`)
	}))
	defer upstream.Close()

	handler := testHandler(testConfig(upstream.URL))
	rec := doTranscription(t, handler, transcriptionRequest{
		fields: map[string]string{"response_format": "text"},
		file:   testWAV,
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type = %q", got)
	}
	if rec.Body.String() != "纯文本" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestTranscriptionsDefaultModelWarning(t *testing.T) {
	var capture requestCapture
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var decoded map[string]any
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		capture.store(decoded)
		_, _ = io.WriteString(w, `{"choices": [{"finish_reason": "stop", "message": {"content": "ok"}}]}`)
	}))
	defer upstream.Close()

	handler := testHandler(testConfig(upstream.URL))
	rec := doTranscription(t, handler, transcriptionRequest{fields: map[string]string{}, file: testWAV})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	captured := capture.load()
	if captured["model"] != "mimo-v2.5-asr" {
		t.Errorf("upstream model = %v", captured["model"])
	}
	if warning := rec.Header().Get("X-Adapter-Warning"); !strings.Contains(warning, "model-defaulted") {
		t.Errorf("X-Adapter-Warning = %q", warning)
	}
}

func TestTranscriptionsLocalErrors(t *testing.T) {
	tests := []struct {
		name     string
		spec     transcriptionRequest
		wantCode string
	}{
		{
			name:     "missing file",
			spec:     transcriptionRequest{fields: map[string]string{"model": "whisper-1"}, omitFile: true},
			wantCode: "missing_file",
		},
		{
			name:     "empty file",
			spec:     transcriptionRequest{fields: map[string]string{"model": "whisper-1"}, file: []byte{}},
			wantCode: "empty_file",
		},
		{
			name:     "webm input",
			spec:     transcriptionRequest{fields: map[string]string{"model": "whisper-1"}, file: []byte{0x1A, 0x45, 0xDF, 0xA3}},
			wantCode: "unsupported_audio_format",
		},
		{
			name: "srt format",
			spec: transcriptionRequest{
				fields: map[string]string{"model": "whisper-1", "response_format": "srt"},
				file:   testWAV,
			},
			wantCode: "unsupported_response_format",
		},
		{
			// 流式只输出增量文本与最终文本，承载不了 verbose_json 的字段，这种组合必须本地拒绝。
			name: "stream with verbose_json",
			spec: transcriptionRequest{
				fields: map[string]string{"model": "whisper-1", "stream": "true", "response_format": "verbose_json"},
				file:   testWAV,
			},
			wantCode: "unsupported_parameter",
		},
		{
			name: "unknown model",
			spec: transcriptionRequest{
				fields: map[string]string{"model": "whisper-large-v3"},
				file:   testWAV,
			},
			wantCode: "model_not_found",
		},
		{
			name:     "missing token",
			spec:     transcriptionRequest{fields: map[string]string{}, file: testWAV, omitAuth: true},
			wantCode: "unauthorized",
		},
		{
			name:     "wrong token",
			spec:     transcriptionRequest{fields: map[string]string{}, file: testWAV, token: "nope"},
			wantCode: "invalid_token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("upstream must not be called for local validation failures")
			}))
			defer upstream.Close()

			handler := testHandler(testConfig(upstream.URL))
			rec := doTranscription(t, handler, tt.spec)

			wantStatus := http.StatusBadRequest
			if tt.wantCode == "unauthorized" || tt.wantCode == "invalid_token" {
				wantStatus = http.StatusUnauthorized
			}
			if rec.Code != wantStatus {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, wantStatus, rec.Body.String())
			}
			if got := errorCode(t, rec.Body.Bytes()); got != tt.wantCode {
				t.Errorf("code = %q, want %q (body %s)", got, tt.wantCode, rec.Body.String())
			}
		})
	}
}

func TestTranscriptionsUpstreamErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		headers    map[string]string
		wantStatus int
		wantCode   string
		wantRetry  string
	}{
		{
			name:       "upstream auth failure",
			status:     http.StatusUnauthorized,
			body:       `{"error": {"message": "invalid api key"}}`,
			wantStatus: http.StatusBadGateway,
			wantCode:   "upstream_auth_error",
		},
		{
			name:       "upstream rate limit",
			status:     http.StatusTooManyRequests,
			body:       `{"error": {"message": "slow down"}}`,
			headers:    map[string]string{"Retry-After": "12"},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "rate_limited",
			wantRetry:  "12",
		},
		{
			name:       "upstream bad request",
			status:     http.StatusBadRequest,
			body:       `{"error": {"message": "audio too long"}}`,
			wantStatus: http.StatusBadGateway,
			wantCode:   "upstream_invalid_request",
		},
		{
			name:       "upstream server error",
			status:     http.StatusInternalServerError,
			body:       `{"error": {"message": "boom"}}`,
			wantStatus: http.StatusBadGateway,
			wantCode:   "upstream_error",
		},
		{
			name:       "invalid json",
			status:     http.StatusOK,
			body:       `this is not json`,
			wantStatus: http.StatusBadGateway,
			wantCode:   "upstream_bad_response",
		},
		{
			name:       "no choices",
			status:     http.StatusOK,
			body:       `{"choices": []}`,
			wantStatus: http.StatusBadGateway,
			wantCode:   "upstream_bad_response",
		},
		{
			name:       "truncated result",
			status:     http.StatusOK,
			body:       `{"choices": [{"finish_reason": "length", "message": {"content": "half"}}]}`,
			wantStatus: http.StatusBadGateway,
			wantCode:   "upstream_truncated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for key, value := range tt.headers {
					w.Header().Set(key, value)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer upstream.Close()

			handler := testHandler(testConfig(upstream.URL))
			rec := doTranscription(t, handler, transcriptionRequest{
				fields: map[string]string{"model": "whisper-1"},
				file:   testWAV,
			})

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if got := errorCode(t, rec.Body.Bytes()); got != tt.wantCode {
				t.Errorf("code = %q, want %q", got, tt.wantCode)
			}
			if tt.wantRetry != "" && rec.Header().Get("Retry-After") != tt.wantRetry {
				t.Errorf("Retry-After = %q, want %q", rec.Header().Get("Retry-After"), tt.wantRetry)
			}
		})
	}
}

func TestTranscriptionsPayloadTooLarge(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called")
	}))
	defer upstream.Close()

	cfg := testConfig(upstream.URL)
	cfg.MaxUploadBytes = 128
	handler := testHandler(cfg)

	large := append([]byte("RIFF\x24\x00\x00\x00WAVE"), bytes.Repeat([]byte{0}, 300)...)
	rec := doTranscription(t, handler, transcriptionRequest{
		fields: map[string]string{"model": "whisper-1"},
		file:   large,
	})

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	if got := errorCode(t, rec.Body.Bytes()); got != "payload_too_large" {
		t.Errorf("code = %q", got)
	}
}

func TestMetaEndpoints(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called by meta endpoints")
	}))
	defer upstream.Close()

	handler := testHandler(testConfig(upstream.URL))

	t.Run("models requires auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
			t.Errorf("WWW-Authenticate = %q", got)
		}
	})

	t.Run("models list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		var body modelsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Object != "list" || len(body.Data) != 4 {
			t.Fatalf("models = %+v", body)
		}
		if body.Data[0].ID != "mimo-v2.5-asr" {
			t.Errorf("first model = %q", body.Data[0].ID)
		}
	})

	t.Run("health is public", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		var body healthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Status != "ok" || body.Version != "test" || body.Commit != "abc123" {
			t.Errorf("health = %+v", body)
		}
	})

	t.Run("unknown route", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/nope", nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d", rec.Code)
		}
		if got := errorCode(t, rec.Body.Bytes()); got != "not_found" {
			t.Errorf("code = %q", got)
		}
	})

	t.Run("wrong method", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/audio/transcriptions", nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d", rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != http.MethodPost {
			t.Errorf("Allow = %q", got)
		}
		if got := errorCode(t, rec.Body.Bytes()); got != "method_not_allowed" {
			t.Errorf("code = %q", got)
		}
	})
}

func TestRequestID(t *testing.T) {
	handler := testHandler(testConfig("http://127.0.0.1:1"))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "trace-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "trace-123" {
		t.Errorf("echoed X-Request-ID = %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "bad id with spaces")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	generated := rec.Header().Get("X-Request-ID")
	if generated == "" || generated == "bad id with spaces" {
		t.Errorf("generated X-Request-ID = %q", generated)
	}
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode error envelope: %v (body %s)", err, body)
	}
	return envelope.Error.Code
}
