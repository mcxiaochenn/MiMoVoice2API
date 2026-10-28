package adapter

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/audio"
	"github.com/mcxiaochenn/MiMoVoice2API/internal/mimo"
)

// 下游支持的转写响应格式。
const (
	ResponseFormatJSON        = "json"
	ResponseFormatText        = "text"
	ResponseFormatVerboseJSON = "verbose_json"
)

// TranscriptionInput 是 httpapi 解析后的转写请求，字段与 OpenAI 表单对应。
type TranscriptionInput struct {
	Audio                  []byte
	Format                 audio.Format
	Model                  string
	Language               string
	ResponseFormat         string
	Prompt                 string
	Temperature            string
	TimestampGranularities []string
	Stream                 bool
}

// TranscriptionPlan 是映射结果：上游请求 + 下游响应所需的元信息。
type TranscriptionPlan struct {
	Request        mimo.ChatRequest
	ResponseFormat string
	Language       string
	Stream         bool
	ModelRequested string
	ModelUpstream  string
	Warnings       []string
}

// BuildTranscription 把 OpenAI 转写请求翻译为 MiMo 的 chat/completions 请求。
func BuildTranscription(in TranscriptionInput, defaultModel string) (*TranscriptionPlan, *Error) {
	model, defaulted, modelErr := ResolveASRModel(in.Model, defaultModel)
	if modelErr != nil {
		return nil, modelErr
	}

	warnings := make([]string, 0, 4)
	if defaulted {
		warnings = append(warnings, "model-defaulted")
	}

	language, fallback := normalizeLanguage(in.Language)
	if fallback {
		warnings = append(warnings, "language-fallback-auto")
	}

	responseFormat, formatErr := normalizeResponseFormat(in.ResponseFormat)
	if formatErr != nil {
		return nil, formatErr
	}

	// 流式只输出增量文本与最终文本，没有位置信息可承载 verbose_json 的字段。
	if in.Stream && responseFormat == ResponseFormatVerboseJSON {
		return nil, &Error{
			Code:    "unsupported_parameter",
			Param:   "response_format",
			Message: "verbose_json is not supported with stream=true; use json or text",
		}
	}

	if err := validateTimestampGranularities(in.TimestampGranularities); err != nil {
		return nil, err
	}

	if strings.TrimSpace(in.Prompt) != "" {
		warnings = append(warnings, "prompt-ignored")
	}
	if raw := strings.TrimSpace(in.Temperature); raw != "" {
		if _, err := strconv.ParseFloat(raw, 64); err != nil {
			return nil, &Error{
				Code:    "unsupported_parameter",
				Param:   "temperature",
				Message: "temperature must be a number when provided",
			}
		}
		warnings = append(warnings, "temperature-ignored")
	}

	request := mimo.ChatRequest{
		Model: model,
		Messages: []mimo.Message{
			{
				Role: "user",
				Content: []mimo.ContentPart{
					{
						Type:       "input_audio",
						InputAudio: &mimo.InputAudio{Data: audio.DataURL(in.Format, in.Audio)},
					},
				},
			},
		},
		ASROptions: &mimo.ASROptions{Language: language},
		Stream:     in.Stream,
	}

	return &TranscriptionPlan{
		Request:        request,
		ResponseFormat: responseFormat,
		Language:       language,
		Stream:         in.Stream,
		ModelRequested: strings.TrimSpace(in.Model),
		ModelUpstream:  model,
		Warnings:       warnings,
	}, nil
}

// normalizeLanguage 把 BCP-47 之类的语言标签收敛到上游支持的三个值。
func normalizeLanguage(raw string) (string, bool) {
	language := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case language == "" || language == "auto":
		return "auto", false
	case language == "zh" || strings.HasPrefix(language, "zh-"):
		return "zh", false
	case language == "en" || strings.HasPrefix(language, "en-"):
		return "en", false
	default:
		return "auto", true
	}
}

func normalizeResponseFormat(raw string) (string, *Error) {
	format := strings.ToLower(strings.TrimSpace(raw))
	if format == "" {
		return ResponseFormatJSON, nil
	}
	switch format {
	case ResponseFormatJSON, ResponseFormatText, ResponseFormatVerboseJSON:
		return format, nil
	case "srt", "vtt":
		return "", &Error{
			Code:    "unsupported_response_format",
			Param:   "response_format",
			Message: fmt.Sprintf("response_format %q is not supported: upstream does not return timestamps", format),
		}
	default:
		return "", &Error{
			Code:    "unsupported_response_format",
			Param:   "response_format",
			Message: fmt.Sprintf("response_format %q is not supported; use json, text or verbose_json", format),
		}
	}
}

func validateTimestampGranularities(values []string) *Error {
	for _, raw := range values {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "", "segment":
			// segment 接受但不输出：上游没有时间戳，api-spec 已声明该差异。
		case "word":
			return &Error{
				Code:    "unsupported_parameter",
				Param:   "timestamp_granularities[]",
				Message: "word-level timestamps are not supported by the upstream model",
			}
		default:
			return &Error{
				Code:    "unsupported_parameter",
				Param:   "timestamp_granularities[]",
				Message: fmt.Sprintf("unknown timestamp granularity %q; only segment is accepted", raw),
			}
		}
	}
	return nil
}
