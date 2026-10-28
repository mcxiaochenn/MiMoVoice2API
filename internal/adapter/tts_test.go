package adapter

import (
	"encoding/base64"
	"slices"
	"testing"
)

func baseSpeechInput() SpeechInput {
	return SpeechInput{
		Model:          "tts-1",
		Input:          "你好，这是一段测试。",
		Voice:          "nova",
		Instructions:   "语气温和。",
		ResponseFormat: "mp3",
	}
}

func TestBuildSpeech(t *testing.T) {
	plan, err := BuildSpeech(baseSpeechInput(), TTSModel, 1000)
	if err != nil {
		t.Fatalf("BuildSpeech() error = %v", err)
	}

	if plan.Request.Model != TTSModel {
		t.Errorf("model = %q", plan.Request.Model)
	}
	if plan.Request.Audio == nil {
		t.Fatal("audio options is nil")
	}
	if plan.Request.Audio.Format != "mp3" || plan.Request.Audio.Voice != "茉莉" {
		t.Errorf("audio = %+v", plan.Request.Audio)
	}
	if plan.ContentType != ContentTypeMP3 {
		t.Errorf("content type = %q", plan.ContentType)
	}
	if plan.ModelRequested != "tts-1" || plan.ModelUpstream != TTSModel {
		t.Errorf("models = %q / %q", plan.ModelRequested, plan.ModelUpstream)
	}
	if len(plan.Warnings) != 0 {
		t.Errorf("warnings = %v", plan.Warnings)
	}
	if len(plan.Request.Messages) != 2 {
		t.Fatalf("messages = %+v", plan.Request.Messages)
	}
	if plan.Request.Messages[0].Role != "user" || plan.Request.Messages[0].Content != "语气温和。" {
		t.Errorf("user message = %+v", plan.Request.Messages[0])
	}
	if plan.Request.Messages[1].Role != "assistant" || plan.Request.Messages[1].Content != "你好，这是一段测试。" {
		t.Errorf("assistant message = %+v", plan.Request.Messages[1])
	}
}

func TestBuildSpeechDefaults(t *testing.T) {
	plan, err := BuildSpeech(SpeechInput{Input: "默认音色"}, "custom-tts", 1000)
	if err != nil {
		t.Fatalf("BuildSpeech() error = %v", err)
	}

	if plan.Request.Model != "custom-tts" {
		t.Errorf("model = %q", plan.Request.Model)
	}
	if plan.Request.Audio.Voice != "mimo_default" {
		t.Errorf("voice = %q", plan.Request.Audio.Voice)
	}
	for _, want := range []string{"model-defaulted", "voice-defaulted"} {
		if !slices.Contains(plan.Warnings, want) {
			t.Errorf("warnings = %v, want %q", plan.Warnings, want)
		}
	}
}

func TestBuildSpeechFormats(t *testing.T) {
	tests := []struct {
		raw         string
		wantFormat  string
		wantType    string
		wantErrCode string
	}{
		{raw: "", wantFormat: "mp3", wantType: ContentTypeMP3},
		{raw: "mp3", wantFormat: "mp3", wantType: ContentTypeMP3},
		{raw: "WAV", wantFormat: "wav", wantType: ContentTypeWAV},
		{raw: "pcm", wantFormat: "pcm16", wantType: ContentTypePCM},
		{raw: "opus", wantErrCode: "unsupported_response_format"},
		{raw: "aac", wantErrCode: "unsupported_response_format"},
		{raw: "flac", wantErrCode: "unsupported_response_format"},
		{raw: "ogg", wantErrCode: "unsupported_response_format"},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			in := baseSpeechInput()
			in.ResponseFormat = tt.raw

			plan, err := BuildSpeech(in, TTSModel, 1000)
			if tt.wantErrCode != "" {
				if err == nil || err.Code != tt.wantErrCode {
					t.Fatalf("error = %+v, want %q", err, tt.wantErrCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if plan.Request.Audio.Format != tt.wantFormat || plan.ContentType != tt.wantType {
				t.Fatalf("format = %q type = %q, want %q / %q", plan.Request.Audio.Format, plan.ContentType, tt.wantFormat, tt.wantType)
			}
		})
	}
}

func TestBuildSpeechValidation(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*SpeechInput)
		maxChars  int
		wantCode  string
		wantParam string
	}{
		{
			name:      "empty input",
			mutate:    func(in *SpeechInput) { in.Input = "   " },
			maxChars:  1000,
			wantCode:  "missing_input",
			wantParam: "input",
		},
		{
			name:      "text too long",
			mutate:    func(in *SpeechInput) { in.Input = "一二三四五" },
			maxChars:  4,
			wantCode:  "text_too_long",
			wantParam: "input",
		},
		{
			name:      "unknown stream format",
			mutate:    func(in *SpeechInput) { in.StreamFormat = "websocket" },
			maxChars:  1000,
			wantCode:  "unsupported_parameter",
			wantParam: "stream_format",
		},
		{
			name:      "unknown stream format",
			mutate:    func(in *SpeechInput) { in.StreamFormat = "chunks" },
			maxChars:  1000,
			wantCode:  "unsupported_parameter",
			wantParam: "stream_format",
		},
		{
			name:      "invalid speed",
			mutate:    func(in *SpeechInput) { in.Speed = `"fast"` },
			maxChars:  1000,
			wantCode:  "unsupported_parameter",
			wantParam: "speed",
		},
		{
			name:      "unknown model",
			mutate:    func(in *SpeechInput) { in.Model = "tts-2" },
			maxChars:  1000,
			wantCode:  "model_not_found",
			wantParam: "model",
		},
		{
			name:      "unknown voice",
			mutate:    func(in *SpeechInput) { in.Voice = "mecha" },
			maxChars:  1000,
			wantCode:  "voice_not_found",
			wantParam: "voice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := baseSpeechInput()
			tt.mutate(&in)
			_, err := BuildSpeech(in, TTSModel, tt.maxChars)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Code != tt.wantCode || err.Param != tt.wantParam {
				t.Fatalf("error = %+v, want %q / %q", err, tt.wantCode, tt.wantParam)
			}
		})
	}
}

func TestBuildSpeechVoiceDesign(t *testing.T) {
	in := SpeechInput{
		Model:        TTSVoiceDesignModel,
		Input:        "欢迎收听今天的节目。",
		Instructions: "中年男性播音员，声音低沉。",
	}
	plan, err := BuildSpeech(in, TTSModel, 1000)
	if err != nil {
		t.Fatalf("BuildSpeech() error = %v", err)
	}
	if plan.Request.Model != TTSVoiceDesignModel {
		t.Errorf("model = %q", plan.Request.Model)
	}
	if plan.Request.Audio.Voice != "" {
		t.Errorf("voice = %q, want empty", plan.Request.Audio.Voice)
	}
	if plan.Request.Audio.Format != "mp3" {
		t.Errorf("format = %q", plan.Request.Audio.Format)
	}
	if len(plan.Request.Messages) != 2 ||
		plan.Request.Messages[0].Content != in.Instructions ||
		plan.Request.Messages[1].Content != in.Input {
		t.Errorf("messages = %+v", plan.Request.Messages)
	}

	// 缺 instructions
	if _, err := BuildSpeech(SpeechInput{Model: TTSVoiceDesignModel, Input: "文本"}, TTSModel, 1000); err == nil || err.Code != "missing_instructions" {
		t.Errorf("missing instructions error = %+v", err)
	}

	// 传 voice 不允许
	if _, err := BuildSpeech(SpeechInput{
		Model:        TTSVoiceDesignModel,
		Input:        "文本",
		Instructions: "描述",
		Voice:        "nova",
	}, TTSModel, 1000); err == nil || err.Code != "unsupported_parameter" {
		t.Errorf("voice with voicedesign error = %+v", err)
	}
}

func TestBuildSpeechVoiceClone(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(wavHeader)
	dataURL := "data:audio/wav;base64," + payload
	tests := []struct {
		name     string
		voice    string
		wantData string
	}{
		// 上游要求 audio.voice 必须是 DataURL，纯 base64 会被拒为 Param Incorrect，
		// 因此两种下游写法都要归一成 DataURL。
		{name: "data url", voice: dataURL, wantData: dataURL},
		{name: "pure base64", voice: payload, wantData: dataURL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := BuildSpeech(SpeechInput{
				Model:        TTSVoiceCloneModel,
				Input:        "克隆音色测试。",
				Voice:        tt.voice,
				Instructions: "这段应该被忽略",
			}, TTSModel, 1000)
			if err != nil {
				t.Fatalf("BuildSpeech() error = %v", err)
			}
			if plan.Request.Audio.Voice != tt.wantData {
				t.Errorf("voice = %q, want %q", plan.Request.Audio.Voice, tt.wantData)
			}
			if len(plan.Request.Messages) != 1 || plan.Request.Messages[0].Role != "assistant" {
				t.Errorf("messages = %+v", plan.Request.Messages)
			}
			if !slices.Contains(plan.Warnings, "instructions-ignored") {
				t.Errorf("warnings = %v", plan.Warnings)
			}
		})
	}

	if _, err := BuildSpeech(SpeechInput{Model: TTSVoiceCloneModel, Input: "文本"}, TTSModel, 1000); err == nil || err.Code != "missing_voice" {
		t.Errorf("missing voice error = %+v", err)
	}
	if _, err := BuildSpeech(SpeechInput{
		Model: TTSVoiceCloneModel,
		Input: "文本",
		Voice: base64.StdEncoding.EncodeToString([]byte("not an audio file")),
	}, TTSModel, 1000); err == nil || err.Code != "unsupported_parameter" {
		t.Errorf("invalid sample error = %+v", err)
	}
}

func TestBuildSpeechSpeed(t *testing.T) {
	plan, err := BuildSpeech(SpeechInput{Model: "tts-1", Input: "文本", Speed: "1"}, TTSModel, 1000)
	if err != nil {
		t.Fatalf("speed 1 error = %v", err)
	}
	if len(plan.Warnings) != 1 || plan.Warnings[0] != "voice-defaulted" {
		t.Errorf("warnings = %v, want only voice-defaulted", plan.Warnings)
	}

	plan, err = BuildSpeech(SpeechInput{Model: "tts-1", Input: "文本", Speed: "1.5"}, TTSModel, 1000)
	if err != nil {
		t.Fatalf("speed 1.5 error = %v", err)
	}
	if !slices.Contains(plan.Warnings, "speed-ignored") {
		t.Errorf("warnings = %v", plan.Warnings)
	}
}

func TestResolveVoice(t *testing.T) {
	tests := []struct {
		raw       string
		want      string
		defaulted bool
		wantErr   bool
	}{
		{raw: "", want: "mimo_default", defaulted: true},
		{raw: "nova", want: "茉莉"},
		{raw: "NOVA", want: "茉莉"},
		{raw: "alloy", want: "冰糖"},
		{raw: "verse", want: "苏打"},
		{raw: "Mia", want: "Mia"},
		{raw: "冰糖", want: "冰糖"},
		{raw: "unknown", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			voice, defaulted, err := resolveVoice(tt.raw)
			if tt.wantErr {
				if err == nil || err.Code != "voice_not_found" {
					t.Fatalf("error = %+v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if voice != tt.want || defaulted != tt.defaulted {
				t.Fatalf("resolveVoice(%q) = (%q, %v), want (%q, %v)", tt.raw, voice, defaulted, tt.want, tt.defaulted)
			}
		})
	}
}

func TestNormalizeVoiceSample(t *testing.T) {
	wavB64 := base64.StdEncoding.EncodeToString(wavHeader)
	mp3B64 := base64.StdEncoding.EncodeToString([]byte("ID3\x04\x00\x00\x00"))
	wavURL := "data:audio/wav;base64," + wavB64
	mp3URL := "data:audio/mpeg;base64," + mp3B64

	if got, err := normalizeVoiceSample(wavURL); err != nil || got != wavURL {
		t.Errorf("wav data URL = (%q, %+v)", got, err)
	}
	if got, err := normalizeVoiceSample(mp3URL); err != nil || got != mp3URL {
		t.Errorf("mp3 data URL = (%q, %+v)", got, err)
	}
	// 纯 base64 要补全前缀，否则上游报 Param Incorrect。
	if got, err := normalizeVoiceSample(wavB64); err != nil || got != wavURL {
		t.Errorf("wav pure base64 = (%q, %+v), want %q", got, err, wavURL)
	}
	if got, err := normalizeVoiceSample(mp3B64); err != nil || got != mp3URL {
		t.Errorf("mp3 pure base64 = (%q, %+v), want %q", got, err, mp3URL)
	}
	// 声明的 MIME 不参与判定，一律按魔数重写。
	if got, err := normalizeVoiceSample("data:audio/ogg;base64," + wavB64); err != nil || got != wavURL {
		t.Errorf("declared MIME should be ignored, got (%q, %+v)", got, err)
	}
	// base64 内的换行要清掉，避免带进上游请求。
	chunked := "data:audio/wav;base64," + wavB64[:8] + "\n" + wavB64[8:]
	if got, err := normalizeVoiceSample(chunked); err != nil || got != wavURL {
		t.Errorf("wrapped base64 = (%q, %+v)", got, err)
	}
	if _, err := normalizeVoiceSample("data:audio/wav;base64,not-base64!"); err == nil {
		t.Error("invalid base64 should be rejected")
	}
	if _, err := normalizeVoiceSample("data:audio/wav;base64," + base64.StdEncoding.EncodeToString([]byte("plain text"))); err == nil {
		t.Error("non-audio payload should be rejected")
	}
	if _, err := normalizeVoiceSample("   "); err == nil || err.Code != "missing_voice" {
		t.Errorf("empty sample error = %+v", err)
	}
}
