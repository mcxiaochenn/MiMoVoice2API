package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/adapter"
	"github.com/mcxiaochenn/MiMoVoice2API/internal/audio"
)

// multipartOverhead 留给边界、字段名与其他表单字段；
// maxMemory 取上传上限 + 余量，确保合法请求不会把文件落到临时目录。
const multipartOverhead = int64(1 << 20)

func (s *server) handleTranscriptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	info := requestInfoFrom(r.Context())

	limit := s.cfg.MaxUploadBytes + multipartOverhead
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := r.ParseMultipartForm(limit); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.writeAPIError(w, r, newAPIError(
				"payload_too_large", "file",
				fmt.Sprintf("upload exceeds the %d byte limit", s.cfg.MaxUploadBytes),
			))
			return
		}
		s.writeAPIError(w, r, newAPIError("invalid_multipart", "", "request body is not valid multipart/form-data"))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	if files := r.MultipartForm.File["file"]; len(files) > 1 {
		s.log.Debug("ignoring extra file parts",
			"request_id", requestIDFrom(r.Context()),
			"extra", len(files)-1,
		)
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			s.writeAPIError(w, r, newAPIError("missing_file", "file", "file field is required"))
			return
		}
		s.writeAPIError(w, r, newAPIError("invalid_multipart", "file", "could not read the file field"))
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, s.cfg.MaxUploadBytes+1))
	if err != nil {
		s.writeAPIError(w, r, newAPIError("invalid_multipart", "file", "could not read the uploaded file"))
		return
	}
	if int64(len(data)) > s.cfg.MaxUploadBytes {
		s.writeAPIError(w, r, newAPIError(
			"payload_too_large", "file",
			fmt.Sprintf("upload exceeds the %d byte limit", s.cfg.MaxUploadBytes),
		))
		return
	}
	if len(data) == 0 {
		s.writeAPIError(w, r, newAPIError("empty_file", "file", "uploaded file is empty"))
		return
	}

	format, ok := audio.Sniff(data)
	if !ok {
		s.writeAPIError(w, r, newAPIError(
			"unsupported_audio_format", "file",
			"unsupported audio format; only mp3 and wav are accepted (transcoding is not performed)",
		))
		return
	}

	stream, ok := parseOptionalBool(r.FormValue("stream"))
	if !ok {
		s.writeAPIError(w, r, newAPIError("unsupported_parameter", "stream", "stream must be true or false"))
		return
	}

	plan, adapterErr := adapter.BuildTranscription(adapter.TranscriptionInput{
		Audio:                  data,
		Format:                 format,
		Model:                  r.FormValue("model"),
		Language:               r.FormValue("language"),
		ResponseFormat:         r.FormValue("response_format"),
		Prompt:                 r.FormValue("prompt"),
		Temperature:            r.FormValue("temperature"),
		TimestampGranularities: timestampGranularities(r),
		Stream:                 stream,
	}, s.cfg.MIMOASRModel)
	if adapterErr != nil {
		s.writeAPIError(w, r, apiErrorFromAdapter(adapterErr))
		return
	}
	info.modelRequested = plan.ModelRequested
	info.modelUpstream = plan.ModelUpstream

	if plan.Stream {
		s.streamTranscription(w, r, plan)
		return
	}

	result, err := s.mimo.Transcribe(r.Context(), plan.Request)
	if err != nil {
		s.writeUpstreamError(w, r, err)
		return
	}
	info.upstreamStatus = result.UpstreamStatus

	if len(plan.Warnings) > 0 {
		w.Header().Set("X-Adapter-Warning", strings.Join(plan.Warnings, "; "))
	}

	switch plan.ResponseFormat {
	case adapter.ResponseFormatText:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, result.Text)
	case adapter.ResponseFormatVerboseJSON:
		verbose := transcriptionVerboseResponse{
			Task:     "transcribe",
			Duration: result.Seconds,
			Text:     result.Text,
		}
		if plan.Language != "auto" {
			verbose.Language = plan.Language
		}
		writeJSON(w, http.StatusOK, verbose)
	default:
		writeJSON(w, http.StatusOK, transcriptionResponse{Text: result.Text})
	}
}

// streamTranscription 把上游增量文本转成 OpenAI 的 transcript.text.* 事件。
// 响应头等到第一个增量到达才写出：上游若在此之前返回 4xx，仍能回标准 JSON 错误。
func (s *server) streamTranscription(w http.ResponseWriter, r *http.Request, plan *adapter.TranscriptionPlan) {
	var sse *sseWriter
	ensure := func() error {
		if sse != nil {
			return nil
		}
		if len(plan.Warnings) > 0 {
			w.Header().Set("X-Adapter-Warning", strings.Join(plan.Warnings, "; "))
		}
		writer, ok := newSSEWriter(w)
		if !ok {
			return errNoFlusher
		}
		sse = writer
		return nil
	}

	result, err := s.mimo.TranscribeStream(r.Context(), plan.Request, func(delta string) error {
		if err := ensure(); err != nil {
			return err
		}
		return sse.event("transcript.text.delta", transcriptDeltaEvent{
			Type:  "transcript.text.delta",
			Delta: delta,
		})
	})
	if err != nil {
		s.finishStream(w, r, sse, err, "transcript.text.error")
		return
	}

	if err := ensure(); err != nil {
		s.finishStream(w, r, nil, err, "transcript.text.error")
		return
	}
	if result.FinishReason == "length" {
		// 文本已经边发边出了，无法回滚，只能记录并让客户端以 done 为准。
		s.log.Warn("streamed transcription truncated by upstream",
			"request_id", requestIDFrom(r.Context()),
			"model", result.Model,
		)
	}
	if err := sse.event("transcript.text.done", transcriptDoneEvent{
		Type:  "transcript.text.done",
		Text:  result.Text,
		Usage: usageFrom(result.Usage),
	}); err != nil {
		s.log.Warn("failed to write transcript done event",
			"request_id", requestIDFrom(r.Context()),
			"error", err,
		)
	}
}

// timestampGranularities 同时接受带方括号与不带方括号的字段名，兼容不同客户端。
func timestampGranularities(r *http.Request) []string {
	if r.MultipartForm == nil {
		return nil
	}
	values := append([]string{}, r.MultipartForm.Value["timestamp_granularities[]"]...)
	return append(values, r.MultipartForm.Value["timestamp_granularities"]...)
}

func parseOptionalBool(raw string) (value bool, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, true
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false
	}
	return parsed, true
}
