package adapter

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/mimo"
)

// 下游 speech 响应格式与对应的 Content-Type。
const (
	SpeechFormatMP3 = "mp3"
	SpeechFormatWAV = "wav"
	SpeechFormatPCM = "pcm"

	ContentTypeMP3 = "audio/mpeg"
	ContentTypeWAV = "audio/wav"
	ContentTypePCM = "audio/pcm"
)

// SpeechInput 是 httpapi 解析后的合成请求。
type SpeechInput struct {
	Model          string
	Input          string
	Voice          string
	Instructions   string
	ResponseFormat string
	Speed          string
	StreamFormat   string
}

// SpeechPlan 是映射结果：上游请求 + 响应所需的元信息。
type SpeechPlan struct {
	Request        mimo.ChatRequest
	ContentType    string
	ModelRequested string
	ModelUpstream  string
	Warnings       []string
}

// BuildSpeech 把 OpenAI 合成请求翻译为 MiMo 的 chat/completions 请求。
func BuildSpeech(in SpeechInput, defaultModel string, maxChars int) (*SpeechPlan, *Error) {
	model, defaulted, modelErr := ResolveTTSModel(in.Model, defaultModel)
	if modelErr != nil {
		return nil, modelErr
	}

	warnings := make([]string, 0, 4)
	if defaulted {
		warnings = append(warnings, "model-defaulted")
	}

	format, contentType, formatErr := normalizeSpeechFormat(in.ResponseFormat)
	if formatErr != nil {
		return nil, formatErr
	}

	if streamErr := validateStreamFormat(in.StreamFormat); streamErr != nil {
		return nil, streamErr
	}

	if strings.TrimSpace(in.Input) == "" {
		return nil, &Error{
			Code:    "missing_input",
			Param:   "input",
			Message: "input must not be empty",
		}
	}
	if length := utf8.RuneCountInString(in.Input); length > maxChars {
		return nil, &Error{
			Code:    "text_too_long",
			Param:   "input",
			Message: fmt.Sprintf("input is too long: %d characters, limit is %d", length, maxChars),
		}
	}

	if speedErr := validateSpeed(in.Speed, &warnings); speedErr != nil {
		return nil, speedErr
	}

	audioOptions := &mimo.AudioOptions{Format: upstreamFormat(format)}
	messages := make([]mimo.Message, 0, 2)

	switch model {
	case TTSVoiceDesignModel:
		if strings.TrimSpace(in.Instructions) == "" {
			return nil, &Error{
				Code:    "missing_instructions",
				Param:   "instructions",
				Message: "instructions is required for " + TTSVoiceDesignModel,
			}
		}
		if strings.TrimSpace(in.Voice) != "" {
			return nil, &Error{
				Code:    "unsupported_parameter",
				Param:   "voice",
				Message: "voice is not supported by " + TTSVoiceDesignModel,
			}
		}
		messages = append(messages, mimo.Message{Role: "user", Content: in.Instructions})

	case TTSVoiceCloneModel:
		sample, sampleErr := normalizeVoiceSample(in.Voice)
		if sampleErr != nil {
			return nil, sampleErr
		}
		audioOptions.Voice = sample
		if strings.TrimSpace(in.Instructions) != "" {
			warnings = append(warnings, "instructions-ignored")
		}

	default:
		voice, voiceDefaulted, voiceErr := resolveVoice(in.Voice)
		if voiceErr != nil {
			return nil, voiceErr
		}
		audioOptions.Voice = voice
		if voiceDefaulted {
			warnings = append(warnings, "voice-defaulted")
		}
		if strings.TrimSpace(in.Instructions) != "" {
			messages = append(messages, mimo.Message{Role: "user", Content: in.Instructions})
		}
	}

	messages = append(messages, mimo.Message{Role: "assistant", Content: in.Input})

	request := mimo.ChatRequest{
		Model:    model,
		Messages: messages,
		Audio:    audioOptions,
		Stream:   false,
	}

	return &SpeechPlan{
		Request:        request,
		ContentType:    contentType,
		ModelRequested: strings.TrimSpace(in.Model),
		ModelUpstream:  model,
		Warnings:       warnings,
	}, nil
}

// upstreamFormat 把下游格式名转成上游枚举：pcm 统一写成 pcm16。
func upstreamFormat(format string) string {
	if format == SpeechFormatPCM {
		return "pcm16"
	}
	return format
}

func normalizeSpeechFormat(raw string) (format string, contentType string, err *Error) {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "", SpeechFormatMP3:
		return SpeechFormatMP3, ContentTypeMP3, nil
	case SpeechFormatWAV:
		return SpeechFormatWAV, ContentTypeWAV, nil
	case SpeechFormatPCM:
		return SpeechFormatPCM, ContentTypePCM, nil
	case "opus", "aac", "flac":
		return "", "", &Error{
			Code:    "unsupported_response_format",
			Param:   "response_format",
			Message: fmt.Sprintf("response_format %q is not supported; upstream only returns mp3, wav or pcm", value),
		}
	default:
		return "", "", &Error{
			Code:    "unsupported_response_format",
			Param:   "response_format",
			Message: fmt.Sprintf("response_format %q is not supported; use mp3, wav or pcm", value),
		}
	}
}

func validateStreamFormat(raw string) *Error {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "audio":
		return nil
	case "sse":
		return &Error{
			Code:    "unsupported_parameter",
			Param:   "stream_format",
			Message: "stream_format \"sse\" is not supported yet; use \"audio\" (planned for M3)",
		}
	default:
		return &Error{
			Code:    "unsupported_parameter",
			Param:   "stream_format",
			Message: "stream_format must be \"audio\"",
		}
	}
}

func validateSpeed(raw string, warnings *[]string) *Error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	speed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return &Error{
			Code:    "unsupported_parameter",
			Param:   "speed",
			Message: "speed must be a number when provided",
		}
	}
	if speed != 1 {
		*warnings = append(*warnings, "speed-ignored")
	}
	return nil
}
