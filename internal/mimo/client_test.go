package mimo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTranscribeTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = io.WriteString(w, `{"choices": [{"message": {"content": "late"}}]}`)
	}))
	defer upstream.Close()

	client := New(upstream.URL, "test-key", 20*time.Millisecond)
	_, err := client.Transcribe(context.Background(), ChatRequest{Model: "mimo-v2.5-asr"})

	var upstreamErr *Error
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("error = %v, want *mimo.Error", err)
	}
	if upstreamErr.Kind != KindTimeout {
		t.Fatalf("kind = %q, want %q", upstreamErr.Kind, KindTimeout)
	}
}

func TestTranscribeTruncated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices": [{"finish_reason": "length", "message": {"content": "half"}}]}`)
	}))
	defer upstream.Close()

	client := New(upstream.URL, "test-key", time.Second)
	_, err := client.Transcribe(context.Background(), ChatRequest{Model: "mimo-v2.5-asr"})

	var upstreamErr *Error
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("error = %v, want *mimo.Error", err)
	}
	if upstreamErr.Kind != KindTruncated {
		t.Fatalf("kind = %q, want %q", upstreamErr.Kind, KindTruncated)
	}
}

func TestRetryOn429HonorsRetryAfter(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error": {"message": "slow down"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices": [{"finish_reason": "stop", "message": {"content": "ok"}}]}`)
	}))
	defer upstream.Close()

	client := New(upstream.URL, "test-key", 5*time.Second)
	out, _, err := client.post(context.Background(), ChatRequest{Model: "mimo-v2.5-asr"})
	if err != nil {
		t.Fatalf("post error = %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if out.Choices[0].Message.Content != "ok" {
		t.Errorf("content = %q", out.Choices[0].Message.Content)
	}
}

func TestNoRetryWithoutRetryAfter(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error": {"message": "rate limited"}}`)
	}))
	defer upstream.Close()

	client := New(upstream.URL, "test-key", 5*time.Second)
	_, err := client.Transcribe(context.Background(), ChatRequest{Model: "mimo-v2.5-asr"})

	var upstreamErr *Error
	if !errors.As(err, &upstreamErr) || upstreamErr.Kind != KindRateLimited {
		t.Fatalf("error = %v, want rate limited", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry without Retry-After)", calls)
	}
}

func TestNoRetryWhenRetryAfterTooLong(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	client := New(upstream.URL, "test-key", 5*time.Second)
	_, err := client.Transcribe(context.Background(), ChatRequest{Model: "mimo-v2.5-asr"})

	var upstreamErr *Error
	if !errors.As(err, &upstreamErr) || upstreamErr.Kind != KindRateLimited {
		t.Fatalf("error = %v, want rate limited", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (wait too long)", calls)
	}
}

func TestRetryOnlyOnce(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	client := New(upstream.URL, "test-key", 5*time.Second)
	_, err := client.Transcribe(context.Background(), ChatRequest{Model: "mimo-v2.5-asr"})

	var upstreamErr *Error
	if !errors.As(err, &upstreamErr) || upstreamErr.Kind != KindRateLimited {
		t.Fatalf("error = %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one retry only)", calls)
	}
}

func TestObserverSeesSuccessAndFailures(t *testing.T) {
	type event struct {
		kind   string
		status int
	}
	var events []event

	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error": {"message": "slow down"}}`)
		default:
			_, _ = io.WriteString(w, `{"choices": [{"finish_reason": "stop", "message": {"content": "ok"}}]}`)
		}
	}))
	defer upstream.Close()

	client := New(upstream.URL, "test-key", 5*time.Second)
	client.SetObserver(func(kind string, status int, _ time.Duration) {
		events = append(events, event{kind: kind, status: status})
	})

	if _, err := client.Transcribe(context.Background(), ChatRequest{Model: "mimo-v2.5-asr"}); err != nil {
		t.Fatalf("Transcribe error = %v", err)
	}

	want := []event{{"rate_limited", 429}, {"ok", 200}}
	if len(events) != len(want) {
		t.Fatalf("events = %+v, want %+v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("event[%d] = %+v, want %+v", i, events[i], want[i])
		}
	}
}

func TestObserverSeesTransportFailure(t *testing.T) {
	var (
		gotKind   string
		gotStatus int = -1
	)
	client := New("http://127.0.0.1:1", "test-key", 2*time.Second)
	client.SetObserver(func(kind string, status int, _ time.Duration) {
		gotKind, gotStatus = kind, status
	})

	_, err := client.Transcribe(context.Background(), ChatRequest{Model: "mimo-v2.5-asr"})
	if err == nil {
		t.Fatal("expected error")
	}
	if gotStatus != 0 {
		t.Errorf("status = %d, want 0", gotStatus)
	}
	if gotKind != string(KindUnreachable) && gotKind != string(KindTimeout) {
		t.Errorf("kind = %q", gotKind)
	}
}

// 流式转写在首片之前就截断（只有 finish_reason=length，没有内容片）。
func TestTranscribeStreamTruncatedBeforeFirstDelta(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":\"length\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"seconds\":3}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	client := New(upstream.URL, "test-key", 5*time.Second)
	calls := 0
	_, err := client.TranscribeStream(context.Background(), ChatRequest{Model: "mimo-v2.5-asr"}, func(string) error {
		calls++
		return nil
	})

	var upstreamErr *Error
	if !errors.As(err, &upstreamErr) || upstreamErr.Kind != KindTruncated {
		t.Fatalf("error = %v, want truncated", err)
	}
	if calls != 0 {
		t.Errorf("delta callbacks = %d, want 0", calls)
	}
}

// 流式转写已经发出内容片后截断：同样必须报错，不允许按成功收尾。
func TestTranscribeStreamTruncatedAfterDelta(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"部分\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	client := New(upstream.URL, "test-key", 5*time.Second)
	var got []string
	_, err := client.TranscribeStream(context.Background(), ChatRequest{Model: "mimo-v2.5-asr"}, func(delta string) error {
		got = append(got, delta)
		return nil
	})

	var upstreamErr *Error
	if !errors.As(err, &upstreamErr) || upstreamErr.Kind != KindTruncated {
		t.Fatalf("error = %v, want truncated", err)
	}
	if len(got) != 1 || got[0] != "部分" {
		t.Errorf("deltas = %q, want [部分]", got)
	}
}

// 正常 stop 的流式转写不受截断判定影响。
func TestTranscribeStreamNormalFinish(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"seconds\":2}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	client := New(upstream.URL, "test-key", 5*time.Second)
	result, err := client.TranscribeStream(context.Background(), ChatRequest{Model: "mimo-v2.5-asr"}, func(string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("TranscribeStream error = %v", err)
	}
	if result.Text != "你好" || result.FinishReason != "stop" || result.Seconds != 2 {
		t.Errorf("result = %+v", result)
	}
}

// 流式合成在首片之前就截断：报 truncated 而不是「无音频」。
func TestSynthesizeStreamTruncatedBeforeFirstChunk(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":\"length\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	client := New(upstream.URL, "test-key", 5*time.Second)
	_, err := client.SynthesizeStream(context.Background(), ChatRequest{Model: "mimo-v2.5-tts"}, func(string) error {
		return nil
	})

	var upstreamErr *Error
	if !errors.As(err, &upstreamErr) || upstreamErr.Kind != KindTruncated {
		t.Fatalf("error = %v, want truncated", err)
	}
}

// 流式合成已经发出音频片后截断：同样报错收尾。
func TestSynthesizeStreamTruncatedAfterChunk(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"audio\":{\"data\":\"QUJD\"}}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	client := New(upstream.URL, "test-key", 5*time.Second)
	var got []string
	_, err := client.SynthesizeStream(context.Background(), ChatRequest{Model: "mimo-v2.5-tts"}, func(chunk string) error {
		got = append(got, chunk)
		return nil
	})

	var upstreamErr *Error
	if !errors.As(err, &upstreamErr) || upstreamErr.Kind != KindTruncated {
		t.Fatalf("error = %v, want truncated", err)
	}
	if len(got) != 1 || got[0] != "QUJD" {
		t.Errorf("chunks = %q, want [QUJD]", got)
	}
}

// 正常 stop 的流式合成不受截断判定影响。
func TestSynthesizeStreamNormalFinish(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"audio\":{\"data\":\"QUJD\"}}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	client := New(upstream.URL, "test-key", 5*time.Second)
	result, err := client.SynthesizeStream(context.Background(), ChatRequest{Model: "mimo-v2.5-tts"}, func(string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("SynthesizeStream error = %v", err)
	}
	if result.Chunks != 1 || result.FinishReason != "stop" {
		t.Errorf("result = %+v", result)
	}
}
