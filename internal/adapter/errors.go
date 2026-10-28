// Package adapter 负责 OpenAI 字段与 MiMo 字段之间的纯映射。
// 它不认识 HTTP，也不读环境变量，便于脱网单测。
package adapter

// Error 是映射阶段的字段级错误，由 httpapi 翻译成 HTTP 状态码。
type Error struct {
	Code    string
	Param   string
	Message string
}

func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}
