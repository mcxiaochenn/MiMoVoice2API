package adapter

import (
	"fmt"
	"strings"
)

const (
	// ASRModel 是 MiMo 语音识别模型。
	ASRModel = "mimo-v2.5-asr"
	// TTSModel 是 MiMo 内置音色合成模型。
	TTSModel = "mimo-v2.5-tts"
	// TTSVoiceDesignModel 用文字描述设计音色。
	TTSVoiceDesignModel = "mimo-v2.5-tts-voicedesign"
	// TTSVoiceCloneModel 用音频样本克隆音色。
	TTSVoiceCloneModel = "mimo-v2.5-tts-voiceclone"
)

// asrAliases 是 OpenAI 转写模型名的存在性兼容表：只保证「名字能用」，
// 语义与识别效果一律以 MiMo 上游为准。
// whisper-1 等旧名在 OpenAI 已进入弃用期（2027-02 下线），但客户端仍在用，保留；
// gpt-transcribe 是 2026-07 起的推荐文件转写模型。
var asrAliases = map[string]struct{}{
	"whisper-1":              {},
	"gpt-4o-transcribe":      {},
	"gpt-4o-mini-transcribe": {},
	"gpt-transcribe":         {},
}

// ttsAliases 同样只做存在性兼容；gpt-4o-mini-tts-2025-12-15 是 OpenAI
// speech 端点接受的快照名。tts-1 系列在 OpenAI 已进入弃用期（2027-01 下线）。
var ttsAliases = map[string]struct{}{
	"tts-1":                      {},
	"tts-1-hd":                   {},
	"gpt-4o-mini-tts":            {},
	"gpt-4o-mini-tts-2025-12-15": {},
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
			"model %q is not supported; use %s, whisper-1, gpt-transcribe, gpt-4o-transcribe or gpt-4o-mini-transcribe",
			model, ASRModel,
		),
	}
}

// ResolveTTSModel 把下游模型名解析为上游 TTS 模型 ID。
func ResolveTTSModel(requested, fallback string) (upstream string, defaulted bool, err *Error) {
	model := strings.TrimSpace(requested)
	if model == "" {
		if fallback == "" {
			fallback = TTSModel
		}
		return fallback, true, nil
	}

	switch model {
	case TTSModel, TTSVoiceDesignModel, TTSVoiceCloneModel:
		return model, false, nil
	}
	if _, ok := ttsAliases[model]; ok {
		return TTSModel, false, nil
	}

	return "", false, &Error{
		Code:  "model_not_found",
		Param: "model",
		Message: fmt.Sprintf(
			"model %q is not supported; use %s, %s, %s, tts-1, tts-1-hd or gpt-4o-mini-tts",
			model, TTSModel, TTSVoiceDesignModel, TTSVoiceCloneModel,
		),
	}
}
