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

// normalizeVoiceSample 把克隆样本统一成上游要求的 DataURL 形式。
// 上游明确要求 audio.voice 必须是 DataURL，纯 base64 会被拒为 Param Incorrect，
// 而下游允许只传 base64，因此这里统一补全前缀；MIME 按魔数判定，不信任声明值。
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
		if !strings.HasSuffix(strings.ToLower(sample[len("data:"):comma]), ";base64") {
			return "", invalidSampleError()
		}
		sample = sample[comma+1:]
	}

	sample = stripWhitespace(sample)
	format, ok := sniffSampleFormat(sample)
	if !ok {
		return "", invalidSampleError()
	}
	return "data:" + format.MIME() + ";base64," + sample, nil
}

func invalidSampleError() *Error {
	return &Error{
		Code:    "unsupported_parameter",
		Param:   "voice",
		Message: "voice clone sample must be base64 encoded mp3 or wav, optionally wrapped in a data URL",
	}
}

// stripWhitespace 去掉 base64 中可能出现的换行与空格，避免拼进上游请求。
func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		}
		return r
	}, s)
}

// sniffSampleFormat 只解码前 64 字节做魔数判断，不展开整个样本。
func sniffSampleFormat(sample string) (audio.Format, bool) {
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(sample))
	buf := make([]byte, 64)
	n, err := io.ReadFull(decoder, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", false
	}
	if n == 0 {
		return "", false
	}
	return audio.Sniff(buf[:n])
}
