package metrics

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func render(t *testing.T, r *Registry) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.Write(&buf); err != nil {
		t.Fatalf("Write error = %v", err)
	}
	return buf.String()
}

func TestCounterRendersLabelsSortedAndEscaped(t *testing.T) {
	r := New()
	r.IncCounter("demo_requests_total", "Total requests.", L("route", "speech"), L("status", "200"))
	r.IncCounter("demo_requests_total", "Total requests.", L("status", "200"), L("route", "speech"))
	r.IncCounter("demo_requests_total", "Total requests.", L("route", `a"b\c`), L("status", "400"))

	out := render(t, r)

	if !strings.Contains(out, "# HELP demo_requests_total Total requests.\n") {
		t.Errorf("missing HELP line:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE demo_requests_total counter\n") {
		t.Errorf("missing TYPE line:\n%s", out)
	}
	// 标签顺序不影响聚合，两次累加应得到 2。
	if !strings.Contains(out, `demo_requests_total{route="speech",status="200"} 2`) {
		t.Errorf("counter aggregation wrong:\n%s", out)
	}
	if !strings.Contains(out, `demo_requests_total{route="a\"b\\c",status="400"} 1`) {
		t.Errorf("label escaping wrong:\n%s", out)
	}
}

func TestGaugeSetAndAdd(t *testing.T) {
	r := New()
	r.SetGauge("demo_in_flight", "In flight.", 5)
	r.AddGauge("demo_in_flight", "In flight.", -2)
	r.SetGauge("demo_build_info", "Build info.", 1, L("version", "dev"))

	out := render(t, r)
	if !strings.Contains(out, "demo_in_flight 3\n") {
		t.Errorf("gauge value wrong:\n%s", out)
	}
	if !strings.Contains(out, `demo_build_info{version="dev"} 1`) {
		t.Errorf("labeled gauge wrong:\n%s", out)
	}
}

func TestHistogramCumulativeBuckets(t *testing.T) {
	r := New()
	for _, v := range []float64{0.004, 0.02, 0.2, 7} {
		r.ObserveHistogram("demo_duration_seconds", "Duration.", v, L("route", "speech"))
	}

	out := render(t, r)

	// 0.004 落入 le=0.005；0.02 落入 le=0.025；0.2 落入 le=0.25；7 落入 le=10。
	for _, want := range []string{
		`demo_duration_seconds_bucket{route="speech",le="0.005"} 1`,
		`demo_duration_seconds_bucket{route="speech",le="0.01"} 1`,
		`demo_duration_seconds_bucket{route="speech",le="0.025"} 2`,
		`demo_duration_seconds_bucket{route="speech",le="0.1"} 2`,
		`demo_duration_seconds_bucket{route="speech",le="0.25"} 3`,
		`demo_duration_seconds_bucket{route="speech",le="5"} 3`,
		`demo_duration_seconds_bucket{route="speech",le="10"} 4`,
		`demo_duration_seconds_bucket{route="speech",le="+Inf"} 4`,
		`demo_duration_seconds_count{route="speech"} 4`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(out, `demo_duration_seconds_sum{route="speech"} 7.224`) {
		t.Errorf("sum wrong:\n%s", out)
	}
}

func TestHistogramWithoutLabels(t *testing.T) {
	r := New()
	r.ObserveHistogram("demo_duration_seconds", "Duration.", 1)

	out := render(t, r)
	if !strings.Contains(out, `demo_duration_seconds_bucket{le="+Inf"} 1`) {
		t.Errorf("unlabeled histogram wrong:\n%s", out)
	}
	if !strings.Contains(out, "demo_duration_seconds_count 1") {
		t.Errorf("unlabeled count wrong:\n%s", out)
	}
}

func TestRuntimeMetricsCollected(t *testing.T) {
	r := New()
	r.CollectRuntime()

	out := render(t, r)
	for _, name := range []string{
		"mimo_voice2api_go_goroutines",
		"mimo_voice2api_go_heap_alloc_bytes",
		"mimo_voice2api_process_start_time_seconds",
	} {
		if !strings.Contains(out, name) {
			t.Errorf("missing runtime metric %q in:\n%s", name, out)
		}
	}
}

func TestTypeConflictDoesNotPanicOrCorrupt(t *testing.T) {
	r := New()
	r.IncCounter("demo_metric", "Counter.")
	r.SetGauge("demo_metric", "Gauge.", 9)

	out := render(t, r)
	if !strings.Contains(out, "# TYPE demo_metric counter\n") {
		t.Errorf("type changed:\n%s", out)
	}
	if !strings.Contains(out, "demo_metric 1\n") {
		t.Errorf("conflicting gauge should be dropped:\n%s", out)
	}
}

func TestConcurrentUpdates(t *testing.T) {
	r := New()

	const goroutines = 16
	const perGoroutine = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				r.IncCounter("demo_requests_total", "Total.", L("route", "speech"))
				r.ObserveHistogram("demo_duration_seconds", "Duration.", 0.01, L("route", "speech"))
			}
		}()
	}
	wg.Wait()

	out := render(t, r)
	if !strings.Contains(out, "demo_requests_total{route=\"speech\"} 3200") {
		t.Errorf("counter lost updates:\n%s", out)
	}
	if !strings.Contains(out, `demo_duration_seconds_count{route="speech"} 3200`) {
		t.Errorf("histogram lost observations:\n%s", out)
	}
}

func TestNilRegistryIsNoop(t *testing.T) {
	var r *Registry
	r.IncCounter("x", "x")
	r.ObserveHistogram("y", "y", 1)
	r.CollectRuntime()
	var buf bytes.Buffer
	if err := r.Write(&buf); err != nil || buf.Len() != 0 {
		t.Errorf("nil registry write = (%d, %v)", buf.Len(), err)
	}
}
