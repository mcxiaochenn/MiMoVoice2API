package mimo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	maxResponseBytes = 32 << 20         // 上游正常响应上限，需容纳 base64 音频
	maxErrorBytes    = 64 << 10         // 上游错误体读取上限
	maxMessageRunes  = 512              // 回传给客户端的上游消息截断长度
	maxRetryAfter    = 10 * time.Second // 429 重试的等待上限，超过就直接失败
	userAgent        = "MiMoVoice2API"
)

// ErrorKind 是归一化后的上游错误类别，由 httpapi 映射为下游错误码。
type ErrorKind string

const (
	KindTimeout        ErrorKind = "timeout"
	KindCanceled       ErrorKind = "canceled"
	KindUnreachable    ErrorKind = "unreachable"
	KindAuth           ErrorKind = "auth"
	KindInvalidRequest ErrorKind = "invalid_request"
	KindRateLimited    ErrorKind = "rate_limited"
	KindServer         ErrorKind = "server"
	KindBadResponse    ErrorKind = "bad_response"
	KindTruncated      ErrorKind = "truncated"
)

// Error 描述一次上游调用的失败。
type Error struct {
	Kind       ErrorKind
	StatusCode int
	Message    string
	RetryAfter string
}

func (e *Error) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("mimo %s (HTTP %d): %s", e.Kind, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("mimo %s: %s", e.Kind, e.Message)
}

// Transcription 是识别结果中下游需要的字段。
type Transcription struct {
	Text           string
	Seconds        int
	Model          string
	FinishReason   string
	Usage          *Usage
	UpstreamStatus int
}

// Speech 是合成结果。非流式保留整段 base64；流式只记录片数与结束原因，
// 音频由调用方在回调中逐片处理，不在内存里拼接。
type Speech struct {
	AudioBase64    string
	Model          string
	FinishReason   string
	Chunks         int
	Usage          *Usage
	UpstreamStatus int
}

// Observer 接收每一次上游尝试的结果，供指标采集使用。
// kind 为 "ok" 或 ErrorKind，status 为 HTTP 状态码（传输失败时为 0）。
// 实现必须并发安全，并应尽快返回：它运行在请求路径上。
type Observer func(kind string, status int, elapsed time.Duration)

// Client 是 MiMo /chat/completions 的最小客户端。
type Client struct {
	baseURL  string
	apiKey   string
	http     *http.Client
	observer Observer
}

// New 构造客户端；timeout 覆盖连接、写入与读取全过程。
func New(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
	}
}

// SetObserver 安装上游观测回调。只应在开始服务前调用。
func (c *Client) SetObserver(fn Observer) {
	c.observer = fn
}

// Transcribe 调用上游完成一次语音识别。
func (c *Client) Transcribe(ctx context.Context, req ChatRequest) (*Transcription, error) {
	out, status, err := c.post(ctx, req)
	if err != nil {
		return nil, err
	}

	seconds := 0
	if out.Usage != nil {
		seconds = out.Usage.Seconds
	}

	return &Transcription{
		Text:           out.Choices[0].Message.Content,
		Seconds:        seconds,
		Model:          out.Model,
		FinishReason:   out.Choices[0].FinishReason,
		UpstreamStatus: status,
	}, nil
}

// Synthesize 调用上游完成一次语音合成。
func (c *Client) Synthesize(ctx context.Context, req ChatRequest) (*Speech, error) {
	out, status, err := c.post(ctx, req)
	if err != nil {
		return nil, err
	}

	audio := out.Choices[0].Message.Audio
	if audio == nil || audio.Data == "" {
		return nil, &Error{
			Kind:       KindBadResponse,
			StatusCode: status,
			Message:    "upstream response contains no audio",
		}
	}

	return &Speech{
		AudioBase64:    audio.Data,
		Model:          out.Model,
		UpstreamStatus: status,
	}, nil
}

// send 负责编码、发送与状态码分类；成功时返回尚未读取的响应体。
// 上游 429 会按 Retry-After 重试一次，等待过长或没给该头则直接失败。
func (c *Client) send(ctx context.Context, req ChatRequest, accept string) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := c.doOnce(ctx, req, accept)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		retryAfter := resp.Header.Get("Retry-After")
		_ = resp.Body.Close()
		upstreamErr := &Error{
			Kind:       classifyStatus(resp.StatusCode),
			StatusCode: resp.StatusCode,
			Message:    upstreamMessage(raw, resp.StatusCode),
			RetryAfter: retryAfter,
		}

		// 只对限流重试，且总共只重试一次，避免在上游压力大时雪上加霜。
		if resp.StatusCode != http.StatusTooManyRequests || attempt > 0 {
			return nil, upstreamErr
		}
		wait, ok := parseRetryAfter(retryAfter)
		if !ok || wait > maxRetryAfter {
			return nil, upstreamErr
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, upstreamErr
		case <-timer.C:
		}
	}
}

// doOnce 负责编码并发出一次请求，不做状态码判断。
func (c *Client) doOnce(ctx context.Context, req ChatRequest, accept string) (*http.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, &Error{Kind: KindBadResponse, Message: "failed to encode upstream request: " + err.Error()}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, &Error{Kind: KindUnreachable, Message: err.Error()}
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", accept)
	httpReq.Header.Set("User-Agent", userAgent)

	start := time.Now()
	resp, err := c.http.Do(httpReq)
	if c.observer != nil {
		if err != nil {
			e := classifyTransportError(err)
			c.observer(string(e.Kind), 0, time.Since(start))
		} else {
			kind := "ok"
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				kind = string(classifyStatus(resp.StatusCode))
			}
			c.observer(kind, resp.StatusCode, time.Since(start))
		}
	}
	if err != nil {
		return nil, classifyTransportError(err)
	}
	return resp, nil
}

// parseRetryAfter 同时支持「秒数」与 HTTP-date 两种写法。
func parseRetryAfter(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	if when, err := http.ParseTime(raw); err == nil {
		wait := time.Until(when)
		if wait < 0 {
			wait = 0
		}
		return wait, true
	}
	return 0, false
}

// post 在 send 基础上解码非流式响应。
func (c *Client) post(ctx context.Context, req ChatRequest) (*ChatResponse, int, error) {
	resp, err := c.send(ctx, req, "application/json")
	if err != nil {
		var upstreamErr *Error
		if errors.As(err, &upstreamErr) {
			return nil, upstreamErr.StatusCode, err
		}
		return nil, 0, err
	}
	defer resp.Body.Close()

	var out ChatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return nil, resp.StatusCode, &Error{
			Kind:       KindBadResponse,
			StatusCode: resp.StatusCode,
			Message:    "invalid JSON from upstream: " + err.Error(),
		}
	}
	if len(out.Choices) == 0 {
		return nil, resp.StatusCode, &Error{
			Kind:       KindBadResponse,
			StatusCode: resp.StatusCode,
			Message:    "upstream response contains no choices",
		}
	}
	if out.Choices[0].FinishReason == "length" {
		return nil, resp.StatusCode, truncatedError(resp.StatusCode)
	}

	return &out, resp.StatusCode, nil
}

// truncatedError 统一 finish_reason=length 的截断错误。
// 非流式与流式共用同一文案，保证错误码、日志与指标的语义一致。
func truncatedError(status int) *Error {
	return &Error{
		Kind:       KindTruncated,
		StatusCode: status,
		Message:    "upstream truncated the result (finish_reason=length); shorten the input and retry",
	}
}

// TranscribeStream 调用上游流式识别，逐片回调文本增量。
// 返回的 Transcription 含拼接后的完整文本，供 done 事件使用。
func (c *Client) TranscribeStream(ctx context.Context, req ChatRequest, onDelta func(string) error) (*Transcription, error) {
	resp, err := c.send(ctx, req, "text/event-stream")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var (
		text   strings.Builder
		model  string
		finish string
		usage  *Usage
	)

	err = readSSE(ctx, resp.Body, func(payload []byte) error {
		var chunk StreamChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return &Error{
				Kind:       KindBadResponse,
				StatusCode: resp.StatusCode,
				Message:    "invalid JSON in upstream stream: " + err.Error(),
			}
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			finish = choice.FinishReason
		}
		if choice.Delta.Content == "" {
			return nil
		}
		text.WriteString(choice.Delta.Content)
		return onDelta(choice.Delta.Content)
	})
	if err != nil {
		return nil, streamError(ctx, err, resp.StatusCode)
	}
	// 与非流式 post() 语义一致：finish_reason=length 表示结果被截断，
	// 不允许伪装成成功完成，由调用方决定补错误事件还是退回 JSON 错误。
	if finish == "length" {
		return nil, truncatedError(resp.StatusCode)
	}

	seconds := 0
	if usage != nil {
		seconds = usage.Seconds
	}
	return &Transcription{
		Text:           text.String(),
		Seconds:        seconds,
		Model:          model,
		FinishReason:   finish,
		Usage:          usage,
		UpstreamStatus: resp.StatusCode,
	}, nil
}

// SynthesizeStream 调用上游流式合成，逐片回调 base64 音频片段。
// 实测分片可独立解码，因此不做跨片缓冲，直接交给调用方写出。
func (c *Client) SynthesizeStream(ctx context.Context, req ChatRequest, onAudio func(string) error) (*Speech, error) {
	resp, err := c.send(ctx, req, "text/event-stream")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var (
		chunks int
		model  string
		finish string
		usage  *Usage
	)

	err = readSSE(ctx, resp.Body, func(payload []byte) error {
		var chunk StreamChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return &Error{
				Kind:       KindBadResponse,
				StatusCode: resp.StatusCode,
				Message:    "invalid JSON in upstream stream: " + err.Error(),
			}
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			finish = choice.FinishReason
		}
		audio := choice.Delta.Audio
		if audio == nil || audio.Data == "" {
			return nil
		}
		chunks++
		return onAudio(audio.Data)
	})
	if err != nil {
		return nil, streamError(ctx, err, resp.StatusCode)
	}
	// 截断判定在「无音频」之前：零分片 + finish_reason=length 是首片前就截断，
	// 应报 truncated 而不是 bad_response，两者客户端应对方式不同。
	if finish == "length" {
		return nil, truncatedError(resp.StatusCode)
	}
	if chunks == 0 {
		return nil, &Error{
			Kind:       KindBadResponse,
			StatusCode: resp.StatusCode,
			Message:    "upstream stream contains no audio",
		}
	}

	return &Speech{
		Model:          model,
		FinishReason:   finish,
		Chunks:         chunks,
		Usage:          usage,
		UpstreamStatus: resp.StatusCode,
	}, nil
}

// streamError 把流读取阶段的中断归一化。
// 客户端主动断开（context 取消）不算上游故障，单独识别。
func streamError(ctx context.Context, err error, status int) error {
	var upstreamErr *Error
	if errors.As(err, &upstreamErr) {
		return err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return classifyTransportError(ctxErr)
	}
	return &Error{
		Kind:       KindBadResponse,
		StatusCode: status,
		Message:    "upstream stream ended unexpectedly: " + err.Error(),
	}
}

func (c *Client) endpoint() string {
	return c.baseURL + "/chat/completions"
}

func classifyTransportError(err error) *Error {
	// 超时与客户端取消要分开：前者是上游问题，后者是调用方主动放弃，
	// 混在一起会让监控把用户断开误判成上游故障。
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Kind: KindTimeout, Message: "request to upstream timed out"}
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Kind: KindCanceled, Message: "request canceled before completion"}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &Error{Kind: KindTimeout, Message: "request to upstream timed out"}
	}
	return &Error{Kind: KindUnreachable, Message: err.Error()}
}

func classifyStatus(status int) ErrorKind {
	switch {
	case status == http.StatusTooManyRequests:
		return KindRateLimited
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return KindAuth
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return KindInvalidRequest
	case status == http.StatusNotFound:
		return KindBadResponse
	case status >= 500:
		return KindServer
	default:
		return KindBadResponse
	}
}

func upstreamMessage(body []byte, status int) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		if msg := strings.TrimSpace(payload.Error.Message); msg != "" {
			return truncateMessage(msg)
		}
		if msg := strings.TrimSpace(payload.Message); msg != "" {
			return truncateMessage(msg)
		}
	}
	if msg := strings.TrimSpace(string(body)); msg != "" {
		return truncateMessage(msg)
	}
	return fmt.Sprintf("upstream returned HTTP %d", status)
}

// truncateMessage 按 rune 截断，避免切出非法 UTF-8，并压平换行。
func truncateMessage(msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	runes := []rune(msg)
	if len(runes) <= maxMessageRunes {
		return msg
	}
	return string(runes[:maxMessageRunes]) + "..."
}
