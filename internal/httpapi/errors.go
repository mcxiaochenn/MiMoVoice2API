package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/adapter"
	"github.com/mcxiaochenn/MiMoVoice2API/internal/mimo"
)

type apiError struct {
	status     int
	typ        string
	code       string
	param      string
	message    string
	retryAfter string
}

type errorSpec struct {
	status int
	typ    string
}

// errorSpecs 把错误码映射为 HTTP 状态与 OpenAI 风格 type。
var errorSpecs = map[string]errorSpec{
	"invalid_multipart":           {http.StatusBadRequest, "invalid_request_error"},
	"invalid_json":                {http.StatusBadRequest, "invalid_request_error"},
	"missing_file":                {http.StatusBadRequest, "invalid_request_error"},
	"empty_file":                  {http.StatusBadRequest, "invalid_request_error"},
	"unsupported_audio_format":    {http.StatusBadRequest, "invalid_request_error"},
	"missing_input":               {http.StatusBadRequest, "invalid_request_error"},
	"text_too_long":               {http.StatusBadRequest, "invalid_request_error"},
	"missing_instructions":        {http.StatusBadRequest, "invalid_request_error"},
	"unsupported_response_format": {http.StatusBadRequest, "invalid_request_error"},
	"unsupported_parameter":       {http.StatusBadRequest, "invalid_request_error"},
	"model_not_found":             {http.StatusBadRequest, "invalid_request_error"},
	"voice_not_found":             {http.StatusBadRequest, "invalid_request_error"},
	"missing_voice":               {http.StatusBadRequest, "invalid_request_error"},
	"unauthorized":                {http.StatusUnauthorized, "authentication_error"},
	"invalid_token":               {http.StatusUnauthorized, "authentication_error"},
	"not_found":                   {http.StatusNotFound, "invalid_request_error"},
	"method_not_allowed":          {http.StatusMethodNotAllowed, "invalid_request_error"},
	"payload_too_large":           {http.StatusRequestEntityTooLarge, "invalid_request_error"},
	"rate_limited":                {http.StatusTooManyRequests, "rate_limit_error"},
	"internal_error":              {http.StatusInternalServerError, "api_error"},
	"upstream_invalid_request":    {http.StatusBadGateway, "api_error"},
	"upstream_auth_error":         {http.StatusBadGateway, "api_error"},
	"upstream_unreachable":        {http.StatusBadGateway, "api_error"},
	"upstream_bad_response":       {http.StatusBadGateway, "api_error"},
	"upstream_truncated":          {http.StatusBadGateway, "api_error"},
	"upstream_error":              {http.StatusBadGateway, "api_error"},
	"upstream_timeout":            {http.StatusGatewayTimeout, "api_error"},
}

func newAPIError(code, param, message string) apiError {
	spec, ok := errorSpecs[code]
	if !ok {
		return apiError{
			status:  http.StatusInternalServerError,
			typ:     "api_error",
			code:    "internal_error",
			message: "internal server error",
		}
	}
	return apiError{status: spec.status, typ: spec.typ, code: code, param: param, message: message}
}

func apiErrorFromAdapter(err *adapter.Error) apiError {
	return newAPIError(err.Code, err.Param, err.Message)
}

func apiErrorFromUpstream(err *mimo.Error) apiError {
	message := err.Message
	if message == "" {
		message = "upstream request failed"
	}

	switch err.Kind {
	case mimo.KindTimeout:
		return apiError{status: http.StatusGatewayTimeout, typ: "api_error", code: "upstream_timeout", message: "upstream request timed out"}
	case mimo.KindUnreachable:
		return apiError{status: http.StatusBadGateway, typ: "api_error", code: "upstream_unreachable", message: "upstream unreachable"}
	case mimo.KindAuth:
		return apiError{status: http.StatusBadGateway, typ: "api_error", code: "upstream_auth_error", message: "upstream authentication failed; verify MIMO_API_KEY"}
	case mimo.KindInvalidRequest:
		return apiError{status: http.StatusBadGateway, typ: "api_error", code: "upstream_invalid_request", message: message}
	case mimo.KindRateLimited:
		return apiError{
			status:     http.StatusTooManyRequests,
			typ:        "rate_limit_error",
			code:       "rate_limited",
			message:    message,
			retryAfter: err.RetryAfter,
		}
	case mimo.KindTruncated:
		return apiError{status: http.StatusBadGateway, typ: "api_error", code: "upstream_truncated", message: message}
	case mimo.KindServer:
		return apiError{status: http.StatusBadGateway, typ: "api_error", code: "upstream_error", message: message}
	default:
		return apiError{status: http.StatusBadGateway, typ: "api_error", code: "upstream_bad_response", message: message}
	}
}

func (s *server) writeAPIError(w http.ResponseWriter, r *http.Request, e apiError) {
	if info := requestInfoFrom(r.Context()); info != nil {
		info.errCode = e.code
	}
	if e.retryAfter != "" {
		w.Header().Set("Retry-After", e.retryAfter)
	}
	if e.status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}

	body := errorEnvelope{Error: errorBody{
		Message: e.message,
		Type:    e.typ,
		Code:    e.code,
	}}
	if e.param != "" {
		param := e.param
		body.Error.Param = &param
	}
	writeJSON(w, e.status, body)
}

// writeUpstreamError 统一记录上游失败细节，并转换成下游错误 envelope。
func (s *server) writeUpstreamError(w http.ResponseWriter, r *http.Request, err error) {
	var upstreamErr *mimo.Error
	if errors.As(err, &upstreamErr) {
		if info := requestInfoFrom(r.Context()); info != nil {
			info.upstreamStatus = upstreamErr.StatusCode
		}
		s.log.Warn("upstream call failed",
			"request_id", requestIDFrom(r.Context()),
			"kind", string(upstreamErr.Kind),
			"upstream_status", upstreamErr.StatusCode,
			"message", upstreamErr.Message,
		)
		s.writeAPIError(w, r, apiErrorFromUpstream(upstreamErr))
		return
	}
	s.writeAPIError(w, r, newAPIError("internal_error", "", "internal server error"))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}
