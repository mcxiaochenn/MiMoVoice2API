package audio

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSniff(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want Format
		ok   bool
	}{
		{name: "wav", data: []byte("RIFF\x24\x00\x00\x00WAVEfmt "), want: FormatWAV, ok: true},
		{name: "wav 恰好 12 字节", data: []byte("RIFF\x24\x00\x00\x00WAVE"), want: FormatWAV, ok: true},
		{name: "mp3 with ID3", data: []byte("ID3\x04\x00\x00\x00"), want: FormatMP3, ok: true},
		{name: "ID3 恰好 3 字节", data: []byte("ID3"), want: FormatMP3, ok: true},
		{name: "mp3 frame sync", data: []byte{0xFF, 0xFB, 0x90, 0x64}, want: FormatMP3, ok: true},
		{name: "aac adts is not mp3", data: []byte{0xFF, 0xF1, 0x50, 0x80}, ok: false},
		{name: "mpeg 保留 layer 组合", data: []byte{0xFF, 0xE0, 0x90, 0x64}, ok: false},
		{name: "mpeg 保留 version 组合", data: []byte{0xFF, 0xEA, 0x90, 0x64}, ok: false},
		{name: "webm", data: []byte{0x1A, 0x45, 0xDF, 0xA3}, ok: false},
		{name: "short riff", data: []byte("RIFF"), ok: false},
		{name: "riff 头不足 12 字节", data: []byte("RIFF\x24\x00\x00\x00WAV"), ok: false},
		{name: "单字节 0xFF", data: []byte{0xFF}, ok: false},
		{name: "同步头但无版本层位", data: []byte{0xFF, 0xE0}, ok: false},
		{name: "空", data: nil, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Sniff(tt.data)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("Sniff() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestDataURL(t *testing.T) {
	payload := []byte("RIFF-data")
	got := DataURL(FormatWAV, payload)

	const prefix = "data:audio/wav;base64,"
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("DataURL() = %q, want prefix %q", got, prefix)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got, prefix))
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if string(decoded) != string(payload) {
		t.Fatalf("decoded = %q, want %q", decoded, payload)
	}
}
