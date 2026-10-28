// Package metrics 提供一个零依赖的 Prometheus 文本格式指标注册表。
//
// 只实现本项目需要的最小能力：计数器、gauge 与直方图。指标名与标签由调用方
// 固定传入，因此不需要处理动态注册；同一指标名的类型必须保持一致。
package metrics

import (
	"fmt"
	"io"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// bucketBounds 是直方图的桶边界，单位由调用方决定（本项目都是秒）。
// 上界取到 300s：长文本合成实测可达 75s，留出余量。
// 不导出：避免调用方在运行时改动，破坏已记录序列的桶数一致性。
var bucketBounds = [...]float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5,
	1, 2.5, 5, 10, 30, 60, 120, 300,
}

type metricType string

const (
	typeCounter   metricType = "counter"
	typeGauge     metricType = "gauge"
	typeHistogram metricType = "histogram"
)

// Label 是一个标签键值对。
type Label struct {
	Name  string
	Value string
}

// L 构造一个标签，纯粹为了让调用点更短。
func L(name, value string) Label {
	return Label{Name: name, Value: value}
}

type series struct {
	// labels 是渲染好的标签串，例如 `{route="speech"}`；无标签时为空。
	labels string
	value  float64
	counts []uint64
	sum    float64
	total  uint64
}

type family struct {
	name   string
	help   string
	typ    metricType
	series map[string]*series
}

// Registry 保存所有指标。所有方法都是并发安全的。
type Registry struct {
	mu           sync.Mutex
	families     map[string]*family
	processStart time.Time
}

// New 创建一个空注册表。
func New() *Registry {
	return &Registry{
		families:     make(map[string]*family),
		processStart: time.Now(),
	}
}

// IncCounter 给计数器加一。
func (r *Registry) IncCounter(name, help string, labels ...Label) {
	r.AddCounter(name, help, 1, labels...)
}

// AddCounter 给计数器加上 delta。
func (r *Registry) AddCounter(name, help string, delta float64, labels ...Label) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.seriesLocked(name, help, typeCounter, labels); s != nil {
		s.value += delta
	}
}

// SetGauge 把 gauge 设为给定值。
func (r *Registry) SetGauge(name, help string, value float64, labels ...Label) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.seriesLocked(name, help, typeGauge, labels); s != nil {
		s.value = value
	}
}

// AddGauge 给 gauge 加上 delta，用于在途请求这类可增可减的量。
func (r *Registry) AddGauge(name, help string, delta float64, labels ...Label) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.seriesLocked(name, help, typeGauge, labels); s != nil {
		s.value += delta
	}
}

// ObserveHistogram 记录一次观测。
func (r *Registry) ObserveHistogram(name, help string, value float64, labels ...Label) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.seriesLocked(name, help, typeHistogram, labels)
	if s == nil {
		return
	}
	// SearchFloat64s 返回第一个 >= value 的桶，符合 Prometheus 的 le 语义。
	if idx := sort.SearchFloat64s(bucketBounds[:], value); idx < len(bucketBounds) {
		s.counts[idx]++
	}
	s.sum += value
	s.total++
}

// CollectRuntime 采集 Go 运行时指标，应在抓取时调用。
func (r *Registry) CollectRuntime() {
	if r == nil {
		return
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	r.SetGauge("mimo_voice2api_go_goroutines", "Number of goroutines that currently exist.", float64(runtime.NumGoroutine()))
	r.SetGauge("mimo_voice2api_go_heap_alloc_bytes", "Bytes of allocated heap objects.", float64(mem.HeapAlloc))
	r.SetGauge("mimo_voice2api_go_heap_sys_bytes", "Bytes of heap memory obtained from the OS.", float64(mem.HeapSys))
	r.SetGauge("mimo_voice2api_process_start_time_seconds", "Start time of the process since unix epoch in seconds.", float64(r.processStart.UnixNano())/1e9)
}

// seriesLocked 取出或创建序列，调用方必须持有 r.mu。
// 返回 nil 表示指标名已被其他类型占用。
func (r *Registry) seriesLocked(name, help string, typ metricType, labels []Label) *series {
	key := renderLabels(labels)

	f := r.families[name]
	if f == nil {
		f = &family{name: name, help: help, typ: typ, series: make(map[string]*series)}
		r.families[name] = f
	} else if f.typ != typ {
		// 同一指标名混用类型是编码错误，Prometheus 也无法解析；
		// 这里丢弃该次观测，让 /metrics 保持可抓取。
		return nil
	}

	s := f.series[key]
	if s == nil {
		s = &series{labels: key, counts: make([]uint64, len(bucketBounds))}
		f.series[key] = s
	}
	return s
}

// Write 按 Prometheus 文本格式输出全部指标。
func (r *Registry) Write(w io.Writer) error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	names := make([]string, 0, len(r.families))
	for name := range r.families {
		names = append(names, name)
	}
	sort.Strings(names)

	type snapSeries struct {
		labels string
		value  float64
		counts []uint64
		sum    float64
		total  uint64
	}
	type snapFamily struct {
		name, help string
		typ        metricType
		series     []snapSeries
	}

	snapshot := make([]snapFamily, 0, len(names))
	for _, name := range names {
		f := r.families[name]
		keys := make([]string, 0, len(f.series))
		for key := range f.series {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		sf := snapFamily{name: f.name, help: f.help, typ: f.typ, series: make([]snapSeries, 0, len(keys))}
		for _, key := range keys {
			s := f.series[key]
			counts := make([]uint64, len(s.counts))
			copy(counts, s.counts)
			sf.series = append(sf.series, snapSeries{
				labels: s.labels,
				value:  s.value,
				counts: counts,
				sum:    s.sum,
				total:  s.total,
			})
		}
		snapshot = append(snapshot, sf)
	}
	r.mu.Unlock()

	var b strings.Builder
	for _, f := range snapshot {
		fmt.Fprintf(&b, "# HELP %s %s\n", f.name, escapeHelp(f.help))
		fmt.Fprintf(&b, "# TYPE %s %s\n", f.name, f.typ)

		for _, s := range f.series {
			switch f.typ {
			case typeHistogram:
				var cumulative uint64
				for i, bound := range bucketBounds {
					cumulative += s.counts[i]
					fmt.Fprintf(&b, "%s_bucket%s %d\n", f.name, withLE(s.labels, formatFloat(bound)), cumulative)
				}
				fmt.Fprintf(&b, "%s_bucket%s %d\n", f.name, withLE(s.labels, "+Inf"), s.total)
				fmt.Fprintf(&b, "%s_sum%s %s\n", f.name, s.labels, formatFloat(s.sum))
				fmt.Fprintf(&b, "%s_count%s %d\n", f.name, s.labels, s.total)
			default:
				fmt.Fprintf(&b, "%s%s %s\n", f.name, s.labels, formatFloat(s.value))
			}
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// renderLabels 按标签名排序后渲染，保证同一组标签得到同一个 key。
func renderLabels(labels []Label) string {
	if len(labels) == 0 {
		return ""
	}
	sorted := make([]Label, len(labels))
	copy(sorted, labels)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var b strings.Builder
	b.WriteByte('{')
	for i, l := range sorted {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(l.Name)
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(l.Value))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// withLE 往已渲染的标签串里补一个 le 标签。
func withLE(labels, le string) string {
	if labels == "" {
		return `{le="` + le + `"}`
	}
	return labels[:len(labels)-1] + `,le="` + le + `"}`
}

func escapeLabelValue(v string) string {
	if !strings.ContainsAny(v, `\"`+"\n") {
		return v
	}
	var b strings.Builder
	for _, r := range v {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func escapeHelp(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), "\n", `\n`)
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}
