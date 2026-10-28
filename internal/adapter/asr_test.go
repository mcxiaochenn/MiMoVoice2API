package adapter

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/audio"
)

var wavHeader = []byte("RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00")

func baseInput() TranscriptionInput {
	return TranscriptionInput{
		Audio:          wavHeader,
		Format:         audio.FormatWAV,
		Model:          "whisper-1",
		Language:       "zh-CN",
		ResponseFormat: "json",
	}
}

func TestBuildTranscription(t *testing.T) {
	plan, err := BuildTranscription(baseInput(), ASRModel)
	if err != nil {
		t.Fatalf("BuildTranscription() error = %v", err)
	}

	if plan.Request.Model != ASRModel {
		t.Errorf("model = %q, want %q", plan.Request.Model, ASRModel)
	}
	if plan.Language != "zh" {
		t.Errorf("language = %q, want zh", plan.Language)
	}
	if plan.ResponseFormat != ResponseFormatJSON {
		t.Errorf("response_format = %q", plan.ResponseFormat)
	}
	if plan.ModelRequested != "whisper-1" {
		t.Errorf("ModelRequested = %q", plan.ModelRequested)
	}
	if len(plan.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", plan.Warnings)
	}
	if plan.Request.Stream {
		t.Error("stream = true, want false")
	}
	if plan.Request.ASROptions == nil || plan.Request.ASROptions.Language != "zh" {
		t.Errorf("asr_options = %+v", plan.Request.ASROptions)
	}
	if len(plan.Request.Messages) != 1 {
		t.Fatalf("messages len = %d", len(plan.Request.Messages))
	}
}

func TestBuildTranscriptionUpstreamJSON(t *testing.T) {
	plan, err := BuildTranscription(baseInput(), ASRModel)
	if err != nil {
		t.Fatalf("BuildTranscription() error = %v", err)
	}

	raw, marshalErr := json.Marshal(plan.Request)
	if marshalErr != nil {
		t.Fatalf("marshal: %v", marshalErr)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if stream, ok := payload["stream"].(bool); !ok || stream {
		t.Errorf("stream = %v, want false", payload["stream"])
	}

	message := payload["messages"].([]any)[0].(map[string]any)
	parts := message["content"].([]any)
	part := parts[0].(map[string]any)
	if part["type"] != "input_audio" {
		t.Errorf("content type = %v", part["type"])
	}

	inputAudio := part["input_audio"].(map[string]any)
	data, _ := inputAudio["data"].(string)
	if !strings.HasPrefix(data, "data:audio/wav;base64,") {
		t.Errorf("input_audio.data = %q", data)
	}
	if _, exists := inputAudio["format"]; exists {
		t.Error("input_audio.format must be omitted to avoid MIME conflicts")
	}

	asrOptions := payload["asr_options"].(map[string]any)
	if asrOptions["language"] != "zh" {
		t.Errorf("asr_options.language = %v", asrOptions["language"])
	}
}

func TestResolveASRModel(t *testing.T) {
	tests := []struct {
		name        string
		requested   string
		fallback    string
		wantModel   string
		wantDefault bool
		wantCode    string
	}{
		{name: "empty uses fallback", requested: "", fallback: "custom-asr", wantModel: "custom-asr", wantDefault: true},
		{name: "native model", requested: ASRModel, fallback: "custom-asr", wantModel: ASRModel},
		{name: "whisper alias", requested: "whisper-1", wantModel: ASRModel},
		{name: "gpt-4o alias", requested: "gpt-4o-transcribe", wantModel: ASRModel},
		{name: "mini alias", requested: "gpt-4o-mini-transcribe", wantModel: ASRModel},
		{name: "gpt-transcribe alias", requested: "gpt-transcribe", wantModel: ASRModel},
		{name: "unknown", requested: "whisper-large", wantCode: "model_not_found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, defaulted, err := ResolveASRModel(tt.requested, tt.fallback)
			if tt.wantCode != "" {
				if err == nil || err.Code != tt.wantCode {
					t.Fatalf("error = %+v, want code %q", err, tt.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if model != tt.wantModel || defaulted != tt.wantDefault {
				t.Fatalf("ResolveASRModel() = (%q, %v), want (%q, %v)", model, defaulted, tt.wantModel, tt.wantDefault)
			}
		})
	}
}

func TestLanguageNormalization(t *testing.T) {
	tests := []struct {
		raw      string
		want     string
		fallback bool
	}{
		{raw: "", want: "auto"},
		{raw: "auto", want: "auto"},
		{raw: "zh", want: "zh"},
		{raw: "zh-CN", want: "zh"},
		{raw: "zh-Hans", want: "zh"},
		{raw: "en", want: "en"},
		{raw: "en-US", want: "en"},
		{raw: "ja", want: "auto", fallback: true},
		{raw: "fr-FR", want: "auto", fallback: true},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, fallback := normalizeLanguage(tt.raw)
			if got != tt.want || fallback != tt.fallback {
				t.Fatalf("normalizeLanguage(%q) = (%q, %v), want (%q, %v)", tt.raw, got, fallback, tt.want, tt.fallback)
			}
		})
	}
}

func TestBuildTranscriptionValidation(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*TranscriptionInput)
		wantCode  string
		wantParam string
	}{
		{
			name:      "srt is unsupported",
			mutate:    func(in *TranscriptionInput) { in.ResponseFormat = "srt" },
			wantCode:  "unsupported_response_format",
			wantParam: "response_format",
		},
		{
			name:      "vtt is unsupported",
			mutate:    func(in *TranscriptionInput) { in.ResponseFormat = "vtt" },
			wantCode:  "unsupported_response_format",
			wantParam: "response_format",
		},
		{
			name:      "unknown response format",
			mutate:    func(in *TranscriptionInput) { in.ResponseFormat = "xml" },
			wantCode:  "unsupported_response_format",
			wantParam: "response_format",
		},
		{
			name: "streaming with verbose_json",
			mutate: func(in *TranscriptionInput) {
				in.Stream = true
				in.ResponseFormat = ResponseFormatVerboseJSON
			},
			wantCode:  "unsupported_parameter",
			wantParam: "response_format",
		},
		{
			name:      "word timestamps unsupported",
			mutate:    func(in *TranscriptionInput) { in.TimestampGranularities = []string{"word"} },
			wantCode:  "unsupported_parameter",
			wantParam: "timestamp_granularities[]",
		},
		{
			name:      "unknown granularity",
			mutate:    func(in *TranscriptionInput) { in.TimestampGranularities = []string{"phoneme"} },
			wantCode:  "unsupported_parameter",
			wantParam: "timestamp_granularities[]",
		},
		{
			name:      "invalid temperature",
			mutate:    func(in *TranscriptionInput) { in.Temperature = "hot" },
			wantCode:  "unsupported_parameter",
			wantParam: "temperature",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := baseInput()
			tt.mutate(&in)
			_, err := BuildTranscription(in, ASRModel)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Code != tt.wantCode || err.Param != tt.wantParam {
				t.Fatalf("error = %+v, want code %q param %q", err, tt.wantCode, tt.wantParam)
			}
		})
	}
}

func TestBuildTranscriptionWarnings(t *testing.T) {
	in := baseInput()
	in.Model = ""
	in.Language = "ja"
	in.Prompt = "专有名词：MiMo"
	in.Temperature = "0.2"
	in.TimestampGranularities = []string{"segment"}

	plan, err := BuildTranscription(in, "default-asr")
	if err != nil {
		t.Fatalf("BuildTranscription() error = %v", err)
	}

	for _, want := range []string{
		"model-defaulted",
		"language-fallback-auto",
		"prompt-ignored",
		"temperature-ignored",
	} {
		if !slices.Contains(plan.Warnings, want) {
			t.Errorf("warnings = %v, want %q", plan.Warnings, want)
		}
	}
	if len(plan.Warnings) != 4 {
		t.Errorf("warnings = %v, want exactly 4", plan.Warnings)
	}
	if plan.Request.Model != "default-asr" {
		t.Errorf("model = %q, want default-asr", plan.Request.Model)
	}
}
