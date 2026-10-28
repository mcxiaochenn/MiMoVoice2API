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

	result, err := s.mimo.Synthesize(r.Context(), plan.Request)
	if err != nil {
		s.writeUpstreamError(w, r, err)
		return
	}
	info.upstreamStatus = result.UpstreamStatus

	w.Header().Set("Content-Type", plan.ContentType)
	if len(plan.Warnings) > 0 {
		w.Header().Set("X-Adapter-Warning", strings.Join(plan.Warnings, "; "))
	}
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, base64.NewDecoder(base64.StdEncoding, strings.NewReader(result.AudioBase64))); err != nil {
		s.log.Warn("failed to write audio response",
			"request_id", requestIDFrom(r.Context()),
			"error", err,
		)
	}
}
