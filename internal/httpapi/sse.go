package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/mimo"
)

// errNoFlusher 表示底层 ResponseWriter 不支持 Flush，无法做流式输出。
var errNoFlusher = errors.New("response writer does not support flushing")

// sseWriter 按 Server-Sent Events 格式写出事件，并在每个事件后立即 flush。
// 上游分片一到就能推到客户端，不做整段缓冲。
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// newSSEWriter 写入 SSE 响应头并返回写出器。
// 响应头一旦发出就无法再改状态码，因此调用方必须在此前完成所有校验。
func newSSEWriter(w http.ResponseWriter) (*sseWriter, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	header := w.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("Connection", "keep-alive")
	// 让 nginx 之类的反代不要缓冲，否则流式会被攒成一次性响应。
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return &sseWriter{w: w, flusher: flusher}, true
}

// event 写出一个命名事件。data 必须是可 JSON 序列化的对象。
func (s *sseWriter) event(name string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if name != "" {
		if _, err := fmt.Fprintf(s.w, "event: %s\n", name); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", data); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// usageFrom 把上游 usage 映射成 OpenAI 的 tokens 结构。
// 上游没有用量时返回 nil，让 omitempty 生效。
func usageFrom(u *mimo.Usage) *usageTokens {
	if u == nil {
		return nil
	}
	return &usageTokens{
		Type:         "tokens",
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
	}
}

// errorBodyFrom 生成流内错误事件里的 error 对象。
// 此时 HTTP 状态码已经发出，只能在事件里表达失败。
func errorBodyFrom(err error) errorBody {
	var upstreamErr *mimo.Error
	if errors.As(err, &upstreamErr) {
		e := apiErrorFromUpstream(upstreamErr)
		return errorBody{Message: e.message, Type: e.typ, Code: e.code}
	}
	return errorBody{Message: "internal server error", Type: "api_error", Code: "internal_error"}
}

// finishStream 统一处理流式过程中的失败。
// 响应头尚未发出时退回标准 JSON 错误；已发出就只能补一个 error 事件，状态码无法再改。
func (s *server) finishStream(w http.ResponseWriter, r *http.Request, sse *sseWriter, err error, eventName string) {
	if errors.Is(err, errNoFlusher) {
		s.writeAPIError(w, r, newAPIError("internal_error", "", "streaming is not supported by this connection"))
		return
	}

	// 客户端主动断开不是故障：上游连接已随 context 一起释放，这里只留一条调试日志。
	if isClientCanceled(err) {
		if info := requestInfoFrom(r.Context()); info != nil {
			info.errCode = "client_canceled"
		}
		s.log.Debug("client closed the stream",
			"request_id", requestIDFrom(r.Context()),
		)
		return
	}

	if sse == nil {
		s.writeUpstreamError(w, r, err)
		return
	}

	body := errorBodyFrom(err)
	if info := requestInfoFrom(r.Context()); info != nil {
		info.errCode = body.Code
		var upstreamErr *mimo.Error
		if errors.As(err, &upstreamErr) {
			info.upstreamStatus = upstreamErr.StatusCode
		}
	}
	s.log.Warn("stream aborted",
		"request_id", requestIDFrom(r.Context()),
		"code", body.Code,
		"error", err,
	)
	if writeErr := sse.event(eventName, streamErrorEvent{Type: eventName, Error: body}); writeErr != nil {
		s.log.Debug("failed to write stream error event",
			"request_id", requestIDFrom(r.Context()),
			"error", writeErr,
		)
	}
}

// isClientCanceled 判断失败是否源自客户端主动断开。
func isClientCanceled(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	var upstreamErr *mimo.Error
	return errors.As(err, &upstreamErr) && upstreamErr.Kind == mimo.KindCanceled
}
