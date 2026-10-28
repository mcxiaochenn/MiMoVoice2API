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
