package adapter

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/audio"
)

// voiceAliases 只保证「存在且可用」，音色语义不与 OpenAI 对齐。
var voiceAliases = map[string]string{
	"alloy":   "冰糖",
	"echo":    "苏打",
	"fable":   "Mia",
	"onyx":    "白桦",
	"nova":    "茉莉",
	"shimmer": "Chloe",
	"ash":     "Milo",
	"ballad":  "Dean",
	"coral":   "冰糖",
	"sage":    "茉莉",
	"verse":   "苏打",
}

// nativeVoices 是上游内置音色，大小写敏感，原样下发。
var nativeVoices = map[string]struct{}{
	"mimo_default": {},
	"冰糖":           {},
	"茉莉":           {},
	"苏打":           {},
	"白桦":           {},
	"Mia":          {},
	"Chloe":        {},
	"Milo":         {},
	"Dean":         {},
}

func resolveVoice(raw string) (voice string, defaulted bool, err *Error) {
	voice = strings.TrimSpace(raw)
	if voice == "" {
		return "mimo_default", true, nil
	}
	if _, ok := nativeVoices[voice]; ok {
		return voice, false, nil
	}
	if mapped, ok := voiceAliases[strings.ToLower(voice)]; ok {
		return mapped, false, nil
	}
	return "", false, &Error{
		Code:  "voice_not_found",
		Param: "voice",
		Message: fmt.Sprintf(
			"voice %q is not supported; use an OpenAI alias such as alloy/nova or a MiMo voice ID",
			voice,
		),
	}
}

// normalizeVoiceSample 把克隆样本统一成上游要求的纯 base64。
// data URL 前缀可带可不带；格式按解码后的魔数判断，只接受 mp3 / wav。
func normalizeVoiceSample(raw string) (string, *Error) {
	sample := strings.TrimSpace(raw)
	if sample == "" {
		return "", &Error{
			Code:    "missing_voice",
			Param:   "voice",
			Message: "voice is required for " + TTSVoiceCloneModel,
		}
	}

	if strings.HasPrefix(strings.ToLower(sample), "data:") {
		comma := strings.Index(sample, ",")
		if comma < 0 {
			return "", invalidSampleError()
		}
		meta := strings.ToLower(sample[len("data:"):comma])
		if !strings.HasSuffix(meta, ";base64") {
			return "", invalidSampleError()
		}
		switch strings.TrimSuffix(meta, ";base64") {
		case "audio/wav", "audio/x-wav", "audio/mpeg", "audio/mp3":
		default:
			return "", invalidSampleError()
		}
		sample = sample[comma+1:]
	}

	if !isBase64Audio(sample) {
		return "", invalidSampleError()
	}
	return sample, nil
}

func invalidSampleError() *Error {
	return &Error{
		Code:    "unsupported_parameter",
		Param:   "voice",
		Message: "voice clone sample must be base64 encoded mp3 or wav; the data URL prefix is optional",
	}
}

// isBase64Audio 只解码前 64 字节做魔数判断，不展开整个样本。
func isBase64Audio(sample string) bool {
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(sample))
	buf := make([]byte, 64)
	n, err := io.ReadFull(decoder, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	if n == 0 {
		return false
	}
	_, ok := audio.Sniff(buf[:n])
	return ok
}
