package adapter

import (
	"fmt"
	"strings"
)

const (
	// ASRModel 是 MiMo 语音识别模型。
	ASRModel = "mimo-v2.5-asr"
)

var asrAliases = map[string]struct{}{
	"whisper-1":              {},
	"gpt-4o-transcribe":      {},
	"gpt-4o-mini-transcribe": {},
}

// ResolveASRModel 把下游模型名解析为上游模型 ID。
// 返回 defaulted 表示请求未指定模型、使用了配置默认值。
func ResolveASRModel(requested, fallback string) (upstream string, defaulted bool, err *Error) {
	model := strings.TrimSpace(requested)
	if model == "" {
		if fallback == "" {
			fallback = ASRModel
		}
		return fallback, true, nil
	}
	if model == ASRModel {
		return model, false, nil
	}
	if _, ok := asrAliases[model]; ok {
		return ASRModel, false, nil
	}
	return "", false, &Error{
		Code:  "model_not_found",
		Param: "model",
		Message: fmt.Sprintf(
			"model %q is not supported; use %s, whisper-1, gpt-4o-transcribe or gpt-4o-mini-transcribe",
			model, ASRModel,
		),
	}
}
