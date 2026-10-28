package mimo

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadSSE(t *testing.T) {
	stream := strings.Join([]string{
		": keepalive comment",
		"",
		"data: {\"a\":1}",
		"",
		"data: {\"b\":2}",
		"data: {\"c\":3}",
		"",
		"event: ignored",
		"data: [DONE]",
		"",
	}, "\n")

	var got []string
	err := readSSE(context.Background(), strings.NewReader(stream), func(payload []byte) error {
		got = append(got, string(payload))
		return nil
	})
	if err != nil {
		t.Fatalf("readSSE error = %v", err)
	}

	want := []string{
		`{"a":1}`,
		// 连续两个 data 行属于同一事件，按规范用换行拼接。
		"{\"b\":2}\n{\"c\":3}",
	}
	if len(got) != len(want) {
		t.Fatalf("events = %d, want %d (%q)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReadSSETrailingEventWithoutBlankLine(t *testing.T) {
	stream := "data: {\"a\":1}\n\ndata: {\"b\":2}\n\ndata: [DONE]"

	var got []string
	if err := readSSE(context.Background(), strings.NewReader(stream), func(payload []byte) error {
		got = append(got, string(payload))
		return nil
	}); err != nil {
		t.Fatalf("readSSE error = %v", err)
	}
	if len(got) != 2 || got[1] != `{"b":2}` {
		t.Fatalf("events = %q", got)
	}
}

func TestReadSSEMissingDoneMeansTruncated(t *testing.T) {
	// 没有 [DONE] 就 EOF，说明连接被切断，不能当成正常结束。
	err := readSSE(context.Background(), strings.NewReader("data: {\"a\":1}\n\n"), func([]byte) error {
		return nil
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReadSSEStopsOnCallbackError(t *testing.T) {
	sentinel := errors.New("stop here")
	stream := "data: 1\n\ndata: 2\n\ndata: 3\n\n"

	calls := 0
	err := readSSE(context.Background(), strings.NewReader(stream), func(payload []byte) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want sentinel", err)
	}
	if calls != 1 {
		t.Errorf("callback calls = %d, want 1", calls)
	}
}

func TestReadSSEHonorsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := readSSE(ctx, strings.NewReader("data: 1\n\ndata: 2\n\n"), func([]byte) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestReadSSELongLine(t *testing.T) {
	// 上游单个音频分片的 base64 可达数十 KB，解析器必须能吃下整行。
	big := strings.Repeat("A", 200*1024)
	stream := "data: {\"x\":\"" + big + "\"}\n\ndata: [DONE]\n\n"

	var size int
	if err := readSSE(context.Background(), strings.NewReader(stream), func(payload []byte) error {
		size = len(payload)
		return nil
	}); err != nil {
		t.Fatalf("readSSE error = %v", err)
	}
	if size < len(big) {
		t.Errorf("payload size = %d, want >= %d", size, len(big))
	}
}
