// Package audio 提供音频格式嗅探与 data URL 构造。
// 只识别上游接受的 mp3 / wav，不做解码或转码。
package audio

import (
	"encoding/base64"
	"strings"
)

// Format 是上游支持的输入音频格式。
type Format string

const (
	FormatMP3 Format = "mp3"
	FormatWAV Format = "wav"
)

// MIME 返回上游 data URL 使用的 MIME 类型。
func (f Format) MIME() string {
	switch f {
	case FormatMP3:
		return "audio/mpeg"
	case FormatWAV:
		return "audio/wav"
	default:
		return "application/octet-stream"
	}
}

// Sniff 根据文件头判断格式，不信任文件名与 multipart 声明的 Content-Type。
func Sniff(data []byte) (Format, bool) {
	// WAV: "RIFF" + 4 字节长度 + "WAVE"
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		return FormatWAV, true
	}
	// MP3: ID3v2 标签
	if len(data) >= 3 && string(data[0:3]) == "ID3" {
		return FormatMP3, true
	}
	// MP3: MPEG 帧同步，排除保留的 version / layer 组合（可挡掉 AAC ADTS）
	if len(data) >= 2 && data[0] == 0xFF && data[1]&0xE0 == 0xE0 {
		version := (data[1] >> 3) & 0x03
		layer := (data[1] >> 1) & 0x03
		if version != 1 && layer != 0 {
			return FormatMP3, true
		}
	}
	return "", false
}

// DataURL 生成 OpenAI 风格的音频 data URL。
func DataURL(format Format, data []byte) string {
	var b strings.Builder
	b.Grow(len(data)*4/3 + 32)
	b.WriteString("data:")
	b.WriteString(format.MIME())
	b.WriteString(";base64,")
	b.WriteString(base64.StdEncoding.EncodeToString(data))
	return b.String()
}
