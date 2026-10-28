package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/adapter"
	"github.com/mcxiaochenn/MiMoVoice2API/internal/mimo"
)

func (s *server) handleSpeech(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	info := requestInfoFrom(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes)
	var req speechRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.writeAPIError(w, r, newAPIError(
				"payload_too_large", "",
				fmt.Sprintf("request body exceeds the %d byte limit", s.cfg.MaxUploadBytes),
			))
			return
		}
		s.writeAPIError(w, r, newAPIError("invalid_json", "", "request body must be a JSON object"))
		return
	}

	plan, adapterErr := adapter.BuildSpeech(adapter.SpeechInput{
		Model:          req.Model,
		Input:          req.Input,
		Voice:          req.Voice,
		Instructions:   req.Instructions,
		ResponseFormat: req.ResponseFormat,
		Speed:          string(req.Speed),
		StreamFormat:   req.StreamFormat,
	}, s.cfg.MIMOTTSModel, s.cfg.MaxTTSChars)
	if adapterErr != nil {
		s.writeAPIError(w, r, apiErrorFromAdapter(adapterErr))
		return
	}
	info.modelRequested = plan.ModelRequested
	info.modelUpstream = plan.ModelUpstream

	s.deliverSpeech(w, r, plan)
}

// deliverSpeech 把合成结果交付给客户端。
// mp3 / pcm 走上游流式，分片到了就转发；wav 的分片是独立完整文件，只能整段下发。
func (s *server) deliverSpeech(w http.ResponseWriter, r *http.Request, plan *adapter.SpeechPlan) {
	info := requestInfoFrom(r.Context())

	var (
		sse        *sseWriter
		headersOut bool
	)

	// writeChunk 按下游格式交付一片音频：SSE 模式发 delta 事件，audio 模式解码后写二进制。
	writeChunk := func(audioBase64 string) error {
		if plan.StreamFormat == adapter.StreamFormatSSE {
			if sse == nil {
				if len(plan.Warnings) > 0 {
					w.Header().Set("X-Adapter-Warning", strings.Join(plan.Warnings, "; "))
				}
				writer, ok := newSSEWriter(w)
				if !ok {
					return errNoFlusher
				}
				sse = writer
			}
			return sse.event("speech.audio.delta", speechDeltaEvent{
				Type:           "speech.audio.delta",
				Audio:          audioBase64,
				ResponseFormat: plan.ResponseFormat,
			})
		}

		if !headersOut {
			w.Header().Set("Content-Type", plan.ContentType)
			if len(plan.Warnings) > 0 {
				w.Header().Set("X-Adapter-Warning", strings.Join(plan.Warnings, "; "))
			}
			w.WriteHeader(http.StatusOK)
			headersOut = true
		}
		if _, err := io.Copy(w, base64.NewDecoder(base64.StdEncoding, strings.NewReader(audioBase64))); err != nil {
			return err
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return nil
	}

	var (
		result *mimo.Speech
		err    error
	)
	if plan.Request.Stream {
		result, err = s.mimo.SynthesizeStream(r.Context(), plan.Request, writeChunk)
	} else {
		result, err = s.mimo.Synthesize(r.Context(), plan.Request)
		if err == nil {
			err = writeChunk(result.AudioBase64)
		}
	}
	if err != nil {
		s.finishStream(w, r, sse, err, "speech.audio.error")
		return
	}
	info.upstreamStatus = result.UpstreamStatus

	// 二进制流没有结束事件，写完即可。
	if plan.StreamFormat != adapter.StreamFormatSSE {
		return
	}
	if sse == nil {
		if len(plan.Warnings) > 0 {
			w.Header().Set("X-Adapter-Warning", strings.Join(plan.Warnings, "; "))
		}
		writer, ok := newSSEWriter(w)
		if !ok {
			s.finishStream(w, r, nil, errNoFlusher, "speech.audio.error")
			return
		}
		sse = writer
	}
	if err := sse.event("speech.audio.done", speechDoneEvent{
		Type:  "speech.audio.done",
		Usage: usageFrom(result.Usage),
	}); err != nil {
		s.log.Warn("failed to write speech done event",
			"request_id", requestIDFrom(r.Context()),
			"error", err,
		)
	}
}
