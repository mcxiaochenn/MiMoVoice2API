package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func doGet(t *testing.T, handler http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestMetricsExposeRequestAndUpstreamSeries(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id": "chatcmpl-1",
			"model": "mimo-v2.5-asr",
			"choices": [{"index": 0, "finish_reason": "stop", "message": {"role": "assistant", "content": "hi"}}],
			"usage": {"seconds": 1}
		}`)
	}))
	defer upstream.Close()

	handler := testHandler(testConfig(upstream.URL))

	if rec := doTranscription(t, handler, transcriptionRequest{
		fields: map[string]string{"model": "whisper-1"},
		file:   testWAV,
	}); rec.Code != http.StatusOK {
		t.Fatalf("transcription status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec := doGet(t, handler, "/metrics", "secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}

	body := rec.Body.String()
	for _, want := range []string{
		`mimo_voice2api_http_requests_total{err_code="none",route="transcriptions",status="200"} 1`,
		`mimo_voice2api_http_request_duration_seconds_count{route="transcriptions"} 1`,
		`mimo_voice2api_http_request_bytes_total{route="transcriptions"}`,
		`mimo_voice2api_http_response_bytes_total{route="transcriptions"}`,
		`mimo_voice2api_upstream_requests_total{kind="ok",status="200"} 1`,
		`mimo_voice2api_upstream_latency_seconds_count{kind="ok"} 1`,
		`mimo_voice2api_build_info{commit="abc123",version="test"} 1`,
		`mimo_voice2api_go_goroutines`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q\n---\n%s", want, body)
		}
	}

	// 抓取 /metrics 的这次请求本身也在途，所以基线是 1 而不是 0；
	// 只要没有泄漏，其他请求结束后都会回落到这个值。
	if !strings.Contains(body, "mimo_voice2api_http_in_flight_requests 1\n") {
		t.Errorf("in-flight gauge not at baseline:\n%s", body)
	}
}

func TestMetricsRecordErrorCode(t *testing.T) {
	handler := testHandler(testConfig("http://127.0.0.1:1"))

	if rec := doSpeech(t, handler, `{"input":"   "}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	body := doGet(t, handler, "/metrics", "secret").Body.String()
	want := `mimo_voice2api_http_requests_total{err_code="missing_input",route="speech",status="400"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("missing %q in:\n%s", want, body)
	}
}

func TestMetricsRequiresAuth(t *testing.T) {
	handler := testHandler(testConfig("http://127.0.0.1:1"))

	rec := doGet(t, handler, "/metrics", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if envelope.Error.Code != "unauthorized" {
		t.Errorf("error code = %q", envelope.Error.Code)
	}
}

func TestMetricsDisabledReturnsNotFound(t *testing.T) {
	cfg := testConfig("http://127.0.0.1:1")
	cfg.MetricsEnabled = false
	handler := testHandler(cfg)

	rec := doGet(t, handler, "/metrics", "secret")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
