package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sseEvent 是下游 SSE 的一条事件。
type sseEvent struct {
	Name string
	Data map[string]any
}

// parseSSE 把下游响应体拆成事件序列，用于断言事件名、顺序与负载。
func parseSSE(t *testing.T, body string) []sseEvent {
	t.Helper()

	var (
		events []sseEvent
		name   string
	)
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			payload := strings.TrimPrefix(line, "data: ")
			if payload == "" || payload == "[DONE]" {
				continue
			}
			var data map[string]any
			if err := json.Unmarshal([]byte(payload), &data); err != nil {
				t.Fatalf("decode event data: %v (%q)", err, payload)
			}
			events = append(events, sseEvent{Name: name, Data: data})
			name = ""
		}
	}
	return events
}

// transcriptionSSE 模拟上游转写流：先给一帧空内容，再逐片下发，最后是 stop、usage 与 [DONE]。
func transcriptionSSE(t *testing.T, w http.ResponseWriter, pieces []string, model string) {
	t.Helper()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	write := func(choices []any, usage any) {
		payload := map[string]any{"id": "cmpl-1", "model": model, "choices": choices}
		if usage != nil {
			payload["usage"] = usage
		}
		data, err := json.Marshal(payload)
		if err != nil {
			t.Errorf("marshal stream chunk: %v", err)
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}

	write([]any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}, "finish_reason": nil}}, nil)
	for _, piece := range pieces {
		write([]any{map[string]any{"index": 0, "delta": map[string]any{"content": piece}, "finish_reason": nil}}, nil)
	}
	write([]any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, nil)
	write([]any{}, map[string]any{
		"prompt_tokens": 39, "completion_tokens": 11, "total_tokens": 50, "seconds": 3,
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func TestTranscriptionStream(t *testing.T) {
	pieces := []string{"你好", "，", "世界"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var decoded map[string]any
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		if decoded["stream"] != true {
			t.Errorf("upstream stream = %v, want true", decoded["stream"])
		}
		transcriptionSSE(t, w, pieces, "mimo-v2.5-asr")
	}))
	defer upstream.Close()

	handler := testHandler(testConfig(upstream.URL))
	rec := doTranscription(t, handler, transcriptionRequest{
		fields: map[string]string{"model": "whisper-1", "stream": "true"},
		file:   testWAV,
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	events := parseSSE(t, rec.Body.String())
	if len(events) != len(pieces)+1 {
		t.Fatalf("events = %d, want %d (%+v)", len(events), len(pieces)+1, events)
	}
	for i, piece := range pieces {
		if events[i].Name != "transcript.text.delta" {
			t.Errorf("event[%d] name = %q", i, events[i].Name)
		}
		if events[i].Data["delta"] != piece {
			t.Errorf("event[%d] delta = %v, want %q", i, events[i].Data["delta"], piece)
		}
	}

	done := events[len(events)-1]
	if done.Name != "transcript.text.done" {
		t.Fatalf("last event = %q, want transcript.text.done", done.Name)
	}
	if done.Data["text"] != strings.Join(pieces, "") {
		t.Errorf("done text = %v", done.Data["text"])
	}
	usage, ok := done.Data["usage"].(map[string]any)
	if !ok {
		t.Fatalf("done usage missing: %v", done.Data)
	}
	if usage["input_tokens"] != float64(39) || usage["total_tokens"] != float64(50) {
		t.Errorf("usage = %v", usage)
	}
	if usage["type"] != "tokens" {
		t.Errorf("usage type = %v", usage["type"])
	}
}

func TestSpeechStreamSSE(t *testing.T) {
	audioBytes := []byte("streamed-audio-payload-for-sse")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var decoded map[string]any
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		audioResponse(t, w, audioBytes, "mimo-v2.5-tts", wantsStream(decoded))
	}))
	defer upstream.Close()

	handler := testHandler(testConfig(upstream.URL))
	rec := doSpeech(t, handler, `{"input":"流式测试","stream_format":"sse","response_format":"mp3"}`, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	events := parseSSE(t, rec.Body.String())
	if len(events) < 2 {
		t.Fatalf("events = %+v", events)
	}

	var joined strings.Builder
	for _, ev := range events[:len(events)-1] {
		if ev.Name != "speech.audio.delta" {
			t.Errorf("unexpected event %q", ev.Name)
			continue
		}
		if ev.Data["response_format"] != "mp3" {
			t.Errorf("response_format = %v", ev.Data["response_format"])
		}
		chunk, _ := ev.Data["audio"].(string)
		joined.WriteString(chunk)
	}

	decoded, err := base64.StdEncoding.DecodeString(joined.String())
	if err != nil {
		t.Fatalf("joined audio is not valid base64: %v", err)
	}
	if string(decoded) != string(audioBytes) {
		t.Errorf("audio = %q, want %q", decoded, audioBytes)
	}

	done := events[len(events)-1]
	if done.Name != "speech.audio.done" {
		t.Errorf("last event = %q, want speech.audio.done", done.Name)
	}
}

func TestSpeechWavKeepsUpstreamNonStreaming(t *testing.T) {
	var capture requestCapture
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var decoded map[string]any
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		capture.store(decoded)
		audioResponse(t, w, []byte("wav-bytes"), "mimo-v2.5-tts", false)
	}))
	defer upstream.Close()

	handler := testHandler(testConfig(upstream.URL))
	rec := doSpeech(t, handler, `{"input":"测试","response_format":"wav"}`, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// wav 的分片是各自独立的完整文件，拼起来不是合法音频，因此必须整段取回。
	if got := capture.load()["stream"]; got != false {
		t.Errorf("upstream stream = %v, want false for wav", got)
	}
}

// 上游一片音频都没给：响应头尚未发出，应当退回标准 JSON 错误。
func TestSpeechStreamNoAudioYieldsJSONError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"c\",\"model\":\"mimo-v2.5-tts\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	handler := testHandler(testConfig(upstream.URL))
	rec := doSpeech(t, handler, `{"input":"测试","stream_format":"sse"}`, true)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body %s)", rec.Code, rec.Body.String())
	}
	if got := errorCode(t, rec.Body.Bytes()); got != "upstream_bad_response" {
		t.Errorf("code = %q", got)
	}
}

// 已经推送了音频片后上游断开：状态码改不了，只能补一个 error 事件。
func TestSpeechStreamUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"c\",\"model\":\"mimo-v2.5-tts\",\"choices\":[{\"index\":0,\"delta\":{\"audio\":{\"id\":\"a\",\"data\":\"QUJD\"}}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		// 之后直接断开：不发 stop、不发 [DONE]、不发 usage。
	}))
	defer upstream.Close()

	handler := testHandler(testConfig(upstream.URL))
	rec := doSpeech(t, handler, `{"input":"测试","stream_format":"sse"}`, true)

	events := parseSSE(t, rec.Body.String())
	if len(events) < 2 {
		t.Fatalf("expected delta then error event, got body %q", rec.Body.String())
	}
	if events[0].Name != "speech.audio.delta" {
		t.Errorf("first event = %q, want speech.audio.delta", events[0].Name)
	}
	last := events[len(events)-1]
	if last.Name != "speech.audio.error" {
		t.Fatalf("last event = %q, want speech.audio.error", last.Name)
	}
	errObj, ok := last.Data["error"].(map[string]any)
	if !ok {
		t.Fatalf("error body missing: %v", last.Data)
	}
	if errObj["code"] == "" || errObj["code"] == nil {
		t.Errorf("error code missing: %v", errObj)
	}
}
