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
