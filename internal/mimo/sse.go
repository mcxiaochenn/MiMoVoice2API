package mimo

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
)

// maxSSELineBytes 是单行 SSE 的上限。上游每个音频分片是一整行
// base64 JSON，实测单行约 41 KB，这里留足余量。
const maxSSELineBytes = 1 << 20

// StreamChunk 是上游流式响应中的一个 chunk。
type StreamChunk struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Choices []StreamChoice `json:"choices"`
	Usage   *Usage         `json:"usage"`
}

// StreamChoice 是 chunk 中的候选，流式下最多一个。
type StreamChoice struct {
	Index        int          `json:"index"`
	Delta        MessageDelta `json:"delta"`
	FinishReason string       `json:"finish_reason"`
}

// MessageDelta 只保留需要的增量字段：文本在 Content，音频在 Audio。
type MessageDelta struct {
	Role    string     `json:"role"`
	Content string     `json:"content"`
	Audio   *AudioData `json:"audio"`
}

// readSSE 按 SSE 规范逐事件解析上游响应，把每个 data 负载交给 onEvent。
// 收到 [DONE] 返回 nil；流在没有 [DONE] 的情况下结束视为连接中断，
// 返回 io.ErrUnexpectedEOF，避免把截断当成正常完成。
func readSSE(ctx context.Context, body io.Reader, onEvent func(payload []byte) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELineBytes)

	var (
		payload  strings.Builder
		seenDone bool
	)
	dispatch := func() error {
		if payload.Len() == 0 {
			return nil
		}
		data := payload.String()
		payload.Reset()
		if data == "[DONE]" {
			seenDone = true
			return io.EOF
		}
		return onEvent([]byte(data))
	}

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()

		switch {
		case line == "":
			// 空行表示一个事件结束。
			if err := dispatch(); err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
		case strings.HasPrefix(line, ":"):
			// 注释行，用于保活，忽略。
		case strings.HasPrefix(line, "data:"):
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			if payload.Len() > 0 {
				// 多行 data 按规范用换行拼接。
				payload.WriteByte('\n')
			}
			payload.WriteString(value)
		default:
			// event: / id: / retry: 等字段本适配器不需要。
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	// 末行没有空行收尾时补一次分发。
	if err := dispatch(); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if !seenDone {
		return io.ErrUnexpectedEOF
	}
	return nil
}
