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

var asrAliases = map[string]struct{}{
	"whisper-1":              {},
	"gpt-4o-transcribe":      {},
	"gpt-4o-mini-transcribe": {},
}

var ttsAliases = map[string]struct{}{
	"tts-1":           {},
	"tts-1-hd":        {},
	"gpt-4o-mini-tts": {},
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
