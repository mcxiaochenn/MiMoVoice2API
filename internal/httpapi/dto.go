package httpapi

import "encoding/json"

// errorEnvelope 是所有失败响应的统一结构。
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    string  `json:"code"`
}

type transcriptionResponse struct {
	Text string `json:"text"`
}

// transcriptionVerboseResponse 里的 language / duration 缺失时省略，
// 因为上游不一定提供，不能拿零值冒充。
type transcriptionVerboseResponse struct {
	Task     string `json:"task"`
	Language string `json:"language,omitempty"`
	Duration int    `json:"duration,omitempty"`
	Text     string `json:"text"`
}

type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int    `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type modelsResponse struct {
	Object string        `json:"object"`
	Data   []modelObject `json:"data"`
}

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// speechRequest 里 Speed 用 RawMessage，便于把「非数字」与「未提供」区分开。
type speechRequest struct {
	Model          string          `json:"model"`
	Input          string          `json:"input"`
	Voice          string          `json:"voice"`
	Instructions   string          `json:"instructions"`
	ResponseFormat string          `json:"response_format"`
	Speed          json.RawMessage `json:"speed"`
	StreamFormat   string          `json:"stream_format"`
}

// 以下为 SSE 事件负载，字段名与 OpenAI 的 speech.audio.* / transcript.text.* 对齐。

// usageTokens 是 OpenAI 的 token 用量对象，type 固定为 tokens。
type usageTokens struct {
	Type         string `json:"type"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	TotalTokens  int    `json:"total_tokens"`
}

type transcriptDeltaEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

type transcriptDoneEvent struct {
	Type  string       `json:"type"`
	Text  string       `json:"text"`
	Usage *usageTokens `json:"usage,omitempty"`
}

type speechDeltaEvent struct {
	Type           string `json:"type"`
	Audio          string `json:"audio"`
	ResponseFormat string `json:"response_format"`
}

type speechDoneEvent struct {
	Type  string       `json:"type"`
	Usage *usageTokens `json:"usage,omitempty"`
}

// streamErrorEvent 在流中途失败时发出，替代对应的 done 事件。
// transcript.text.* 与 speech.audio.* 共用同一结构，只有 type 不同。
type streamErrorEvent struct {
	Type  string    `json:"type"`
	Error errorBody `json:"error"`
}
