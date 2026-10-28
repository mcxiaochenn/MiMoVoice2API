package mimo

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// FuzzReadSSE 保证 SSE 解析器对任意输入不 panic、不挂死。
// 种子语料覆盖多行 data、注释、缺 [DONE]、空 data、超长行与残缺事件；
// go test 会把种子当普通用例执行，不要求开启 fuzzing。
func FuzzReadSSE(f *testing.F) {
	f.Add("data: {\"a\":1}\n\ndata: [DONE]\n\n")
	f.Add("data: [DONE]")
	f.Add("data: [DONE]\n\n")
	f.Add(": keepalive\n\ndata: 1\n\n")
	f.Add("data: a\ndata: b\n\n")
	f.Add("data: 1\n\n") // 缺 [DONE]，应报截断而不是 panic
	f.Add("event: x\nid: 1\nretry: 100\ndata: [DONE]\n\n")
	f.Add("data:")
	f.Add("data: \n\n")
	f.Add("\n\n\n")
	f.Add("data: [DONE]\n\ndata: after-done\n\n")
	f.Add("data:" + strings.Repeat("A", 70000)) // 超过单行上限的残缺行
	f.Add("da" + strings.Repeat("ta: x\n", 50))

	f.Fuzz(func(t *testing.T, stream string) {
		// 正常回调：只要不 panic 即可，返回值不作断言。
		_ = readSSE(context.Background(), strings.NewReader(stream), func([]byte) error {
			return nil
		})
		// 回调立刻报错：解析器应停止并透传错误，同样不允许 panic。
		sentinel := errors.New("stop")
		err := readSSE(context.Background(), strings.NewReader(stream), func([]byte) error {
			return sentinel
		})
		if err != nil && !errors.Is(err, sentinel) {
			// 事件流可能在触发回调前就以 EOF/超长行结束，此时返回的是解析错误。
			t.Logf("readSSE ended with: %v", err)
		}
	})
}
