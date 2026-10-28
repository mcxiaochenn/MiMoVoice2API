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
