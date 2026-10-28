package mimo

// ChatRequest 是发往 /v1/chat/completions 的请求体。
// 语音识别与语音合成都复用这个结构。
type ChatRequest struct {
	Model      string        `json:"model"`
	Messages   []Message     `json:"messages"`
	ASROptions *ASROptions   `json:"asr_options,omitempty"`
	Audio      *AudioOptions `json:"audio,omitempty"`
	Stream     bool          `json:"stream"`
}

// Message 的 Content 既可以是字符串（合成），也可以是内容片段数组（识别）。
type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// ContentPart 是消息内容中的一个片段，目前只用到 input_audio。
type ContentPart struct {
	Type       string      `json:"type"`
	InputAudio *InputAudio `json:"input_audio,omitempty"`
}

// InputAudio 使用 data URL 传递音频；Format 留空，避免与 data URL 的 MIME 冲突。
type InputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format,omitempty"`
}

// ASROptions 是识别专用参数。
type ASROptions struct {
	Language string `json:"language"`
}

// AudioOptions 是合成专用参数。Format 必须显式下发：
// 上游在 stream=true 时默认改为 pcm，不写会与响应声明的格式不符。
type AudioOptions struct {
	Format string `json:"format"`
	Voice  string `json:"voice,omitempty"`
}

// ChatResponse 是上游非流式响应中我们关心的部分。
type ChatResponse struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage"`
}

// Choice 是单个候选结果。
type Choice struct {
	Index        int             `json:"index"`
	FinishReason string          `json:"finish_reason"`
	Message      ResponseMessage `json:"message"`
}

// ResponseMessage 保留文本字段与可选的音频结果。
type ResponseMessage struct {
	Role    string     `json:"role"`
	Content string     `json:"content"`
	Audio   *AudioData `json:"audio,omitempty"`
}

// AudioData 是合成返回的 base64 音频。
type AudioData struct {
	ID   string `json:"id"`
	Data string `json:"data"`
}

// Usage 记录上游用量，Seconds 用于 verbose_json 的 duration。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	Seconds          int `json:"seconds"`
}
