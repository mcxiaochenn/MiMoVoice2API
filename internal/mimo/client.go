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
	"strings"
	"time"
)

const (
	maxResponseBytes = 4 << 20  // 上游正常响应上限
	maxErrorBytes    = 64 << 10 // 上游错误体读取上限
	maxMessageRunes  = 512      // 回传给客户端的上游消息截断长度
	userAgent        = "MiMoVoice2API"
)

// ErrorKind 是归一化后的上游错误类别，由 httpapi 映射为下游错误码。
type ErrorKind string

const (
	KindTimeout        ErrorKind = "timeout"
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
	UpstreamStatus int
}

// Client 是 MiMo /chat/completions 的最小客户端。
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New 构造客户端；timeout 覆盖连接、写入与读取全过程。
func New(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
	}
}

// Transcribe 调用上游完成一次语音识别。
func (c *Client) Transcribe(ctx context.Context, req ChatRequest) (*Transcription, error) {
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
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, classifyTransportError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		return nil, &Error{
			Kind:       classifyStatus(resp.StatusCode),
			StatusCode: resp.StatusCode,
			Message:    upstreamMessage(raw, resp.StatusCode),
			RetryAfter: resp.Header.Get("Retry-After"),
		}
	}

	var out ChatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return nil, &Error{
			Kind:       KindBadResponse,
			StatusCode: resp.StatusCode,
			Message:    "invalid JSON from upstream: " + err.Error(),
		}
	}
	if len(out.Choices) == 0 {
		return nil, &Error{
			Kind:       KindBadResponse,
			StatusCode: resp.StatusCode,
			Message:    "upstream response contains no choices",
		}
	}

	choice := out.Choices[0]
	if choice.FinishReason == "length" {
		return nil, &Error{
			Kind:       KindTruncated,
			StatusCode: resp.StatusCode,
			Message:    "upstream truncated the result (finish_reason=length); split the input and retry",
		}
	}

	seconds := 0
	if out.Usage != nil {
		seconds = out.Usage.Seconds
	}

	return &Transcription{
		Text:           choice.Message.Content,
		Seconds:        seconds,
		Model:          out.Model,
		FinishReason:   choice.FinishReason,
		UpstreamStatus: resp.StatusCode,
	}, nil
}

func (c *Client) endpoint() string {
	return c.baseURL + "/chat/completions"
}

func classifyTransportError(err error) *Error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &Error{Kind: KindTimeout, Message: "request to upstream timed out or was canceled"}
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
