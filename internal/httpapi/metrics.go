package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/mcxiaochenn/MiMoVoice2API/internal/metrics"
)

// 指标名集中在这里，避免调用点写错字符串。
const (
	metricHTTPRequests      = "mimo_voice2api_http_requests_total"
	metricHTTPDuration      = "mimo_voice2api_http_request_duration_seconds"
	metricHTTPRequestBytes  = "mimo_voice2api_http_request_bytes_total"
	metricHTTPResponseBytes = "mimo_voice2api_http_response_bytes_total"
	metricHTTPInFlight      = "mimo_voice2api_http_in_flight_requests"
	metricUpstreamRequests  = "mimo_voice2api_upstream_requests_total"
	metricUpstreamLatency   = "mimo_voice2api_upstream_latency_seconds"
	metricBuildInfo         = "mimo_voice2api_build_info"
)

// handleMetrics 输出 Prometheus 文本格式指标。
// 与 /healthz 不同，这个端点默认需要鉴权：指标会暴露调用量，
// 抓取端可以用 authorization.bearer_token 配置同一个 AUTH_TOKEN。
func (s *server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		s.handleNotFound(w, r)
		return
	}

	s.metrics.CollectRuntime()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if err := s.metrics.Write(w); err != nil {
		s.log.Debug("failed to write metrics",
			"request_id", requestIDFrom(r.Context()),
			"error", err,
		)
	}
}

// observeRequest 记录一次下游请求。errCode 为空表示成功。
func (s *server) observeRequest(info *requestInfo, status int, outBytes int64, elapsed time.Duration) {
	if s.metrics == nil || info == nil {
		return
	}
	errCode := info.errCode
	if errCode == "" {
		errCode = "none"
	}
	s.metrics.IncCounter(metricHTTPRequests,
		"Total HTTP requests handled, by route, status code and error code.",
		metrics.L("route", info.route),
		metrics.L("status", strconv.Itoa(status)),
		metrics.L("err_code", errCode),
	)
	s.metrics.ObserveHistogram(metricHTTPDuration,
		"HTTP request duration in seconds, by route.",
		elapsed.Seconds(),
		metrics.L("route", info.route),
	)
	if info.inBytes > 0 {
		s.metrics.AddCounter(metricHTTPRequestBytes,
			"Bytes read from request bodies, by route.",
			float64(info.inBytes),
			metrics.L("route", info.route),
		)
	}
	s.metrics.AddCounter(metricHTTPResponseBytes,
		"Bytes written to response bodies, by route.",
		float64(outBytes),
		metrics.L("route", info.route),
	)
}

// observeUpstream 记录每一次上游尝试，包含 429 重试的第二次尝试。
// 注意它统计的是「尝试次数」而不是「下游请求数」。
func (s *server) observeUpstream(kind string, status int, elapsed time.Duration) {
	if s.metrics == nil {
		return
	}
	if kind == "" {
		kind = "ok"
	}
	s.metrics.IncCounter(metricUpstreamRequests,
		"Upstream attempts, by normalized result kind and HTTP status.",
		metrics.L("kind", kind),
		metrics.L("status", strconv.Itoa(status)),
	)
	s.metrics.ObserveHistogram(metricUpstreamLatency,
		"Upstream time to response headers in seconds, by result kind.",
		elapsed.Seconds(),
		metrics.L("kind", kind),
	)
}
