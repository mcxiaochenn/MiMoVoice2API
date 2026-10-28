package httpapi

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/config"
)

// startBlockingUpstream 启动假上游：每有请求到达就通知 arrived，随后阻塞到 release 被调用。
// 用它把并发时序钉死：额度被占住的窗口完全可控，不靠 sleep 猜调度。
func startBlockingUpstream(t *testing.T) (srv *httptest.Server, arrived <-chan struct{}, release func()) {
	t.Helper()
	arrivedCh := make(chan struct{}, 16)
	releaseCh := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(releaseCh) }) }
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrivedCh <- struct{}{}
		<-releaseCh
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(func() {
		release()
		srv.Close()
	})
	return srv, arrivedCh, release
}

// limitConfig 在 testConfig 基础上覆盖并发额度。
func limitConfig(upstreamURL string, max int) config.Config {
	cfg := testConfig(upstreamURL)
	cfg.MaxConcurrentRequests = max
	return cfg
}

// newTranscriptionReq 构造一个合法的转写请求，供并发用例在 goroutine 里使用
// （t.Fatalf 不能在非测试 goroutine 调用，所以构造阶段留在测试主流程）。
func newTranscriptionReq(t *testing.T) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("model", "whisper-1"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "sample.wav")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := part.Write(testWAV); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer secret")
	return req
}

// 额度耗尽后立刻 503 server_busy；请求结束后额度必须回到可用状态。
func TestConcurrencyLimitRejectsOverCapacity(t *testing.T) {
	srv, arrived, release := startBlockingUpstream(t)
	handler := testHandler(limitConfig(srv.URL, 1))

	// 请求 A 占住唯一额度并阻塞在上游。
	reqA := newTranscriptionReq(t)
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, reqA)
		firstDone <- rec
	}()
	<-arrived

	// B / C 在额度已满时到达，必须快速拒绝而不是排队。
	for _, name := range []string{"B", "C"} {
		rec := doTranscription(t, handler, transcriptionRequest{
			fields: map[string]string{"model": "whisper-1"},
			file:   testWAV,
		})
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("request %s status = %d, want 503 (body %s)", name, rec.Code, rec.Body.String())
		}
		if got := errorCode(t, rec.Body.Bytes()); got != "server_busy" {
			t.Fatalf("request %s code = %q, want server_busy", name, got)
		}
	}

	// A 完成后额度释放，新请求可以再进入。
	release()
	if rec := <-firstDone; rec.Code != http.StatusOK {
		t.Fatalf("request A status = %d, body %s", rec.Code, rec.Body.String())
	}
	rec := doTranscription(t, handler, transcriptionRequest{
		fields: map[string]string{"model": "whisper-1"},
		file:   testWAV,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("request D status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// /healthz 不占额度：业务请求占满时健康检查仍可用。
func TestHealthzBypassesConcurrencyLimit(t *testing.T) {
	srv, arrived, release := startBlockingUpstream(t)
	handler := testHandler(limitConfig(srv.URL, 1))

	reqA := newTranscriptionReq(t)
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, reqA)
	}()
	<-arrived

	rec := doGet(t, handler, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	release()
	<-firstDone
}

// 客户端中途取消后额度必须释放。
func TestConcurrencyPermitReleasedAfterClientCancel(t *testing.T) {
	srv, arrived, release := startBlockingUpstream(t)
	handler := testHandler(limitConfig(srv.URL, 1))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := newTranscriptionReq(t).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}()
	<-arrived

	cancel()
	<-done

	// 上游对已取消的请求收尾放行，后续请求应能拿到额度。
	release()
	rec := doTranscription(t, handler, transcriptionRequest{
		fields: map[string]string{"model": "whisper-1"},
		file:   testWAV,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("follow-up status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// 处理器返回错误（如 400）后额度必须释放。
func TestConcurrencyPermitReleasedAfterHandlerError(t *testing.T) {
	srv, arrived, release := startBlockingUpstream(t)
	handler := testHandler(limitConfig(srv.URL, 1))

	// 缺 file 字段的请求在 handler 内 400，不经过上游，也要释放额度。
	rec := doTranscription(t, handler, transcriptionRequest{
		fields:   map[string]string{"model": "whisper-1"},
		omitFile: true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	// 额度空闲，正常请求可以进入（随后阻塞在上游，测完放行）。
	reqNext := newTranscriptionReq(t)
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		next := httptest.NewRecorder()
		handler.ServeHTTP(next, reqNext)
	}()
	<-arrived
	release()
	<-firstDone
}

// 二进制音频流以 http.ErrAbortHandler 中止（panic 穿透中间件）后额度必须释放。
func TestConcurrencyPermitReleasedAfterStreamAbort(t *testing.T) {
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		// 第一片音频正常写出。
		_, _ = w.Write([]byte("data: {\"id\":\"c\",\"model\":\"mimo-v2.5-tts\",\"choices\":[{\"index\":0,\"delta\":{\"audio\":{\"id\":\"a\",\"data\":\"QUJD\"}}}]}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		if atomic.AddInt32(&calls, 1) == 1 {
			// 第一次请求随后直接断开：触发二进制流中止。
			return
		}
		// 后续请求给完整收尾，验证额度已释放。
		_, _ = w.Write([]byte("data: {\"id\":\"c\",\"model\":\"mimo-v2.5-tts\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	handler := testHandler(limitConfig(upstream.URL, 1))

	// 第一次：写出音频后上游中断 → ErrAbortHandler。
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"测试"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		handler.ServeHTTP(rec, req)
	}()
	if recovered != http.ErrAbortHandler {
		t.Fatalf("panic = %v, want http.ErrAbortHandler", recovered)
	}

	// 第二次：额度必须已经释放，否则这里会 503。
	rec = doSpeech(t, handler, `{"input":"再来一次","response_format":"mp3"}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("follow-up status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "ABC" {
		t.Errorf("follow-up body = %q, want decoded audio chunk", got)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("upstream calls = %d, want 2", calls)
	}
}
