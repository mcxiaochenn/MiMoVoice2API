package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func doSpeech(t *testing.T, handler http.Handler, body string, withAuth bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if withAuth {
		req.Header.Set("Authorization", "Bearer secret")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func audioResponse(t *testing.T, w http.ResponseWriter, payload []byte, model string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":    "chatcmpl-audio",
		"model": model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"finish_reason": "stop",
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"audio": map[string]any{
						"id":   "audio-1",
						"data": base64.StdEncoding.EncodeToString(payload),
					},
				},
			},
		},
	})
}

func TestSpeechSuccess(t *testing.T) {
	audioBytes := []byte("fake-mp3-bytes")
	var capture requestCapture
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("upstream Authorization = %q", got)
		}
		var decoded map[string]any
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Errorf("decode upstream request: %v", err)
		} else {
			capture.store(decoded)
		}
		audioResponse(t, w, audioBytes, "mimo-v2.5-tts")
	}))
	defer upstream.Close()

	handler := testHandler(testConfig(upstream.URL))
	rec := doSpeech(t, handler, `{
		"model": "tts-1",
		"input": "你好，这是一段测试。",
		"voice": "nova",
		"instructions": "语气温和。",
		"response_format": "mp3"
	}`, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "audio/mpeg" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	if rec.Header().Get("X-Adapter-Warning") != "" {
		t.Errorf("unexpected warning: %q", rec.Header().Get("X-Adapter-Warning"))
	}
	if rec.Body.String() != string(audioBytes) {
		t.Errorf("audio body = %q, want %q", rec.Body.String(), audioBytes)
	}

	captured := capture.load()
	if captured["model"] != "mimo-v2.5-tts" {
		t.Errorf("upstream model = %v", captured["model"])
	}
	if captured["stream"] != false {
		t.Errorf("upstream stream = %v", captured["stream"])
	}

	audioOptions := captured["audio"].(map[string]any)
	if audioOptions["format"] != "mp3" || audioOptions["voice"] != "茉莉" {
		t.Errorf("audio = %v", audioOptions)
	}

	messages := captured["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %v", messages)
	}
	if messages[0].(map[string]any)["role"] != "user" || messages[0].(map[string]any)["content"] != "语气温和。" {
		t.Errorf("user message = %v", messages[0])
	}
	if messages[1].(map[string]any)["role"] != "assistant" || messages[1].(map[string]any)["content"] != "你好，这是一段测试。" {
		t.Errorf("assistant message = %v", messages[1])
	}
}

func TestSpeechFormats(t *testing.T) {
	tests := []struct {
		format      string
		wantType    string
		wantContent string
	}{
		{format: "mp3", wantType: "audio/mpeg"},
		{format: "wav", wantType: "audio/wav"},
		{format: "pcm", wantType: "audio/pcm"},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				audioResponse(t, w, []byte("audio-"+tt.format), "mimo-v2.5-tts")
			}))
			defer upstream.Close()

			handler := testHandler(testConfig(upstream.URL))
			rec := doSpeech(t, handler, `{"input":"测试","response_format":"`+tt.format+`"}`, true)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != tt.wantType {
				t.Errorf("Content-Type = %q, want %q", got, tt.wantType)
			}
			if rec.Body.String() != "audio-"+tt.format {
				t.Errorf("body = %q", rec.Body.String())
			}
		})
	}
}

func TestSpeechVoiceDesign(t *testing.T) {
	var capture requestCapture
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var decoded map[string]any
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		capture.store(decoded)
		audioResponse(t, w, []byte("design"), "mimo-v2.5-tts-voicedesign")
	}))
	defer upstream.Close()

	handler := testHandler(testConfig(upstream.URL))
	rec := doSpeech(t, handler, `{
		"model": "mimo-v2.5-tts-voicedesign",
		"input": "欢迎收听今天的节目。",
		"instructions": "中年男性播音员，声音低沉。"
	}`, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	captured := capture.load()
	audioOptions := captured["audio"].(map[string]any)
	if _, exists := audioOptions["voice"]; exists {
		t.Errorf("voicedesign must not send voice: %v", audioOptions)
	}
	messages := captured["messages"].([]any)
	if len(messages) != 2 || messages[0].(map[string]any)["role"] != "user" {
		t.Errorf("messages = %v", messages)
	}
}

func TestSpeechVoiceClone(t *testing.T) {
	sample := base64.StdEncoding.EncodeToString(testWAV)
	var capture requestCapture
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var decoded map[string]any
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		capture.store(decoded)
		audioResponse(t, w, []byte("clone"), "mimo-v2.5-tts-voiceclone")
	}))
	defer upstream.Close()

	handler := testHandler(testConfig(upstream.URL))
	rec := doSpeech(t, handler, `{
		"model": "mimo-v2.5-tts-voiceclone",
		"input": "克隆音色测试。",
		"voice": "data:audio/wav;base64,`+sample+`",
		"instructions": "这条会被忽略"
	}`, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if warning := rec.Header().Get("X-Adapter-Warning"); !strings.Contains(warning, "instructions-ignored") {
		t.Errorf("X-Adapter-Warning = %q", warning)
	}

	captured := capture.load()
	audioOptions := captured["audio"].(map[string]any)
	// 上游要求 DataURL，适配层不能把前缀剥掉。
	if want := "data:audio/wav;base64," + sample; audioOptions["voice"] != want {
		t.Errorf("voice = %v, want data URL", audioOptions["voice"])
	}
	messages := captured["messages"].([]any)
	if len(messages) != 1 || messages[0].(map[string]any)["role"] != "assistant" {
		t.Errorf("messages = %v", messages)
	}
}

func TestSpeechLocalErrors(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		maxChars  int
		withAuth  bool
		wantCode  string
		wantParam string
	}{
		{name: "missing input", body: `{"voice":"nova"}`, maxChars: 1000, withAuth: true, wantCode: "missing_input", wantParam: "input"},
		{name: "text too long", body: `{"input":"12345"}`, maxChars: 4, withAuth: true, wantCode: "text_too_long", wantParam: "input"},
		{name: "unknown model", body: `{"model":"tts-9","input":"x"}`, maxChars: 1000, withAuth: true, wantCode: "model_not_found", wantParam: "model"},
		{name: "unknown voice", body: `{"input":"x","voice":"mecha"}`, maxChars: 1000, withAuth: true, wantCode: "voice_not_found", wantParam: "voice"},
		{name: "opus format", body: `{"input":"x","response_format":"opus"}`, maxChars: 1000, withAuth: true, wantCode: "unsupported_response_format", wantParam: "response_format"},
		{name: "sse stream", body: `{"input":"x","stream_format":"sse"}`, maxChars: 1000, withAuth: true, wantCode: "unsupported_parameter", wantParam: "stream_format"},
		{name: "speed not a number", body: `{"input":"x","speed":"fast"}`, maxChars: 1000, withAuth: true, wantCode: "unsupported_parameter", wantParam: "speed"},
		{name: "voicedesign without instructions", body: `{"model":"mimo-v2.5-tts-voicedesign","input":"x"}`, maxChars: 1000, withAuth: true, wantCode: "missing_instructions", wantParam: "instructions"},
		{name: "voiceclone without voice", body: `{"model":"mimo-v2.5-tts-voiceclone","input":"x"}`, maxChars: 1000, withAuth: true, wantCode: "missing_voice", wantParam: "voice"},
		{name: "invalid json", body: `{`, maxChars: 1000, withAuth: true, wantCode: "invalid_json"},
		{name: "missing token", body: `{"input":"x"}`, maxChars: 1000, withAuth: false, wantCode: "unauthorized"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("upstream must not be called for local validation failures")
			}))
			defer upstream.Close()

			cfg := testConfig(upstream.URL)
			cfg.MaxTTSChars = tt.maxChars
			handler := testHandler(cfg)
			rec := doSpeech(t, handler, tt.body, tt.withAuth)

			wantStatus := http.StatusBadRequest
			if tt.wantCode == "unauthorized" {
				wantStatus = http.StatusUnauthorized
			}
			if rec.Code != wantStatus {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, wantStatus, rec.Body.String())
			}
			if got := errorCode(t, rec.Body.Bytes()); got != tt.wantCode {
				t.Errorf("code = %q, want %q", got, tt.wantCode)
			}
			if tt.wantParam != "" {
				var envelope struct {
					Error struct {
						Param *string `json:"param"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
					t.Fatalf("decode envelope: %v", err)
				}
				if envelope.Error.Param == nil || *envelope.Error.Param != tt.wantParam {
					t.Errorf("param = %v, want %q", envelope.Error.Param, tt.wantParam)
				}
			}
		})
	}
}

func TestSpeechUpstreamErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "upstream auth",
			status:     http.StatusUnauthorized,
			body:       `{"error":{"message":"bad key"}}`,
			wantStatus: http.StatusBadGateway,
			wantCode:   "upstream_auth_error",
		},
		{
			name:       "upstream no audio",
			status:     http.StatusOK,
			body:       `{"choices":[{"finish_reason":"stop","message":{"content":""}}]}`,
			wantStatus: http.StatusBadGateway,
			wantCode:   "upstream_bad_response",
		},
		{
			name:       "upstream server error",
			status:     http.StatusBadGateway,
			body:       `{"error":{"message":"boom"}}`,
			wantStatus: http.StatusBadGateway,
			wantCode:   "upstream_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer upstream.Close()

			handler := testHandler(testConfig(upstream.URL))
			rec := doSpeech(t, handler, `{"input":"测试"}`, true)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if got := errorCode(t, rec.Body.Bytes()); got != tt.wantCode {
				t.Errorf("code = %q, want %q", got, tt.wantCode)
			}
		})
	}
}

func TestSpeechPayloadTooLarge(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called")
	}))
	defer upstream.Close()

	cfg := testConfig(upstream.URL)
	cfg.MaxUploadBytes = 64
	handler := testHandler(cfg)
	rec := doSpeech(t, handler, `{"input":"`+strings.Repeat("a", 200)+`"}`, true)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	if got := errorCode(t, rec.Body.Bytes()); got != "payload_too_large" {
		t.Errorf("code = %q", got)
	}
}
