# API 规范

> 本文件是下游契约与上游映射的**唯一事实来源**。代码与本文不一致视为 bug。
> 上游契约核对日期：2026-10-28（MiMo 开放平台「Speech Recognition」「Speech Synthesis」文档）。

## 1. 通用约定

### 1.1 基址与鉴权

- 基址：`http://<host>:8080/v1`，与 OpenAI SDK 的 `base_url` 对齐
- 鉴权：`Authorization: Bearer <AUTH_TOKEN>`；`AUTH_TOKEN` 为空时不校验（仅限本机调试）
- 上游鉴权：服务端使用 `Authorization: Bearer <MIMO_API_KEY>`，密钥不出服务端

### 1.2 请求头

| 头 | 方向 | 说明 |
| --- | --- | --- |
| `Authorization` | 入 | 下游 token，可选（取决于配置） |
| `X-Request-ID` | 入/出 | 不超过 64 位且仅含字母、数字、`-`、`_`、`.` 时复用，否则生成；始终回写 |
| `X-Adapter-Warning` | 出 | 降级提示，分号分隔的 ASCII token，见 1.5 |
| `Cache-Control` | 出 | 固定 `no-store` |

### 1.3 宽松与严格

| 情况 | 策略 |
| --- | --- |
| 缺 `model` | 用 `MIMO_ASR_MODEL` / `MIMO_TTS_MODEL` 兜底，回 `model-defaulted` 警告 |
| 未知 `model`（非空且不在别名表） | 400 `model_not_found` |
| 请求体里的未知字段 | 忽略，不报错 |
| 已支持字段取到未知值 | 400，指出 `param` |
| 缺 `voice` | 用 `mimo_default`，回 `voice-defaulted` 警告 |
| 未知 `voice` | 400 `voice_not_found` |

原则：**结构宽松、取值严格**。多传字段不惩罚，传错值必须暴露。

### 1.4 错误响应

统一 envelope，`Content-Type: application/json`：

```json
{
  "error": {
    "message": "response_format 'srt' is not supported: upstream does not return timestamps",
    "type": "invalid_request_error",
    "param": "response_format",
    "code": "unsupported_response_format"
  }
}
```

`param` 在不适用时为 `null`。

### 1.5 降级警告 token

`X-Adapter-Warning` 的值是分号分隔的固定 token，便于客户端断言：

| token | 触发 |
| --- | --- |
| `model-defaulted` | 请求未带 `model` |
| `voice-defaulted` | 请求未带 `voice` |
| `speed-ignored` | `speed` 存在且 ≠ 1.0 |
| `language-fallback-auto` | `language` 不是 `auto` / `zh*` / `en*` |
| `instructions-ignored` | 音色克隆模型下传了 `instructions` |
| `prompt-ignored` | 转写请求带 `prompt` |
| `temperature-ignored` | 转写请求带 `temperature` |

## 2. 语音转写 `POST /v1/audio/transcriptions`

`Content-Type: multipart/form-data`

### 2.1 请求字段

| 字段 | 类型 | 必填 | 支持度 | 规则 |
| --- | --- | --- | --- | --- |
| `file` | 二进制 | 是 | 支持 | 仅 `mp3` / `wav`，按魔数判定；大小受 `MAX_UPLOAD_BYTES` 限制 |
| `model` | string | 否 | 支持 | 别名与原生 ID 见 2.4；空则用默认值 |
| `language` | string | 否 | 部分 | `zh*` → `zh`，`en*` → `en`，其它 → `auto` + 警告 |
| `response_format` | string | 否 | 部分 | `json`（默认）/ `text` / `verbose_json`；`srt`、`vtt` 返回 400 |
| `prompt` | string | 否 | 忽略 | 上游无此能力，仅记长度，回 `prompt-ignored` |
| `temperature` | number | 否 | 忽略 | 上游无采样控制，回 `temperature-ignored` |
| `timestamp_granularities[]` | string[] | 否 | 部分 | `segment` 接受但无输出；`word` 返回 400 |
| `stream` | bool | 否 | M3 | M1/M2 传 `true` 返回 400 |
| 其它 | — | 否 | 忽略 | 例如 `include[]`、`chunking_strategy` |

多文件（重复 `file`）不支持：只取第一个，其余忽略并记 debug 日志。

### 2.2 示例

```bash
curl -sS http://127.0.0.1:8080/v1/audio/transcriptions \
  -H "Authorization: Bearer $AUTH_TOKEN" \
  -F "file=@testdata/audio/sample-1s.wav" \
  -F "model=whisper-1" \
  -F "language=zh" \
  -F "response_format=verbose_json"
```

### 2.3 响应

`response_format=json`（默认）：

```json
{ "text": "今天天气不错。" }
```

`response_format=text`：`Content-Type: text/plain; charset=utf-8`，body 为纯文本，无 JSON 包装。
OpenAI 在纯文本响应末尾不加换行，本服务保持一致。

`response_format=verbose_json`：

```json
{
  "task": "transcribe",
  "language": "zh",
  "duration": 3,
  "text": "今天天气不错。"
}
```

- `duration` 取上游 `usage.seconds`（整数秒）；缺失则省略该字段
- `language` 取实际下发给上游的值；`auto` 时省略（上游不返回检测结果）
- 不含 `segments`（上游无时间戳）

### 2.4 模型路由

| 下游 `model` | 上游 `model` |
| --- | --- |
| 空 | `MIMO_ASR_MODEL`（默认 `mimo-v2.5-asr`） |
| `whisper-1` / `gpt-4o-transcribe` / `gpt-4o-mini-transcribe` | `mimo-v2.5-asr` |
| `mimo-v2.5-asr` | 原样 |
| 其它 | 400 `model_not_found` |

### 2.5 上行映射

```json
{
  "model": "mimo-v2.5-asr",
  "messages": [
    {
      "role": "user",
      "content": [
        {
          "type": "input_audio",
          "input_audio": {
            "data": "data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEA..."
          }
        }
      ]
    }
  ],
  "asr_options": { "language": "zh" },
  "stream": false
}
```

| 下游 | 上游 | 说明 |
| --- | --- | --- |
| `file` | `messages[0].content[0].input_audio.data` | `data:<mime>;base64,<...>`；**不传 `format`**，避免 MIME 与 format 冲突 |
| `language` | `asr_options.language` | 仅 `auto` / `zh` / `en` |
| — | `messages[0].role` | 固定 `user`，只放一段音频 |
| — | `stream` | M1/M2 固定 `false` |

## 3. 语音合成 `POST /v1/audio/speech`

`Content-Type: application/json`

### 3.1 请求字段

| 字段 | 类型 | 必填 | 支持度 | 规则 |
| --- | --- | --- | --- | --- |
| `model` | string | 否 | 支持 | 别名与三个原生模型见 3.4；空则用默认值 |
| `input` | string | 是 | 支持 | 1 ~ `MAX_TTS_CHARS`（默认 1000）字符，超限 400 |
| `voice` | string | 否（克隆模型必填） | 部分 | 别名、原生音色 ID，或克隆样本（仅克隆模型） |
| `instructions` | string | 否（音色设计必填） | 部分 | 映射为 `user` 消息；克隆模型下忽略并警告 |
| `response_format` | string | 否 | 部分 | `mp3`（默认）/ `wav` / `pcm`；其它 400 |
| `speed` | number | 否 | 忽略 | 非数字 400；≠ 1.0 时回 `speed-ignored` |
| `stream_format` | string | 否 | M3 | M2 只接受 `audio`；`sse` 返回 400 |
| 其它 | — | 否 | 忽略 | |

JSON 请求体上限同样使用 `MAX_UPLOAD_BYTES`（25 MiB），主要留给音色克隆样本。

缺 `voice` 时的行为按模型区分：普通合成回落 `mimo_default` 并回 `voice-defaulted`；音色设计不传该字段；音色克隆必须提供，否则 400 `missing_voice`。

### 3.2 示例与响应

```bash
curl -sS http://127.0.0.1:8080/v1/audio/speech \
  -H "Authorization: Bearer $AUTH_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "tts-1",
    "voice": "nova",
    "input": "你好，这是一段语音合成测试。",
    "instructions": "语气温和，语速稍慢。",
    "response_format": "mp3"
  }' --output out.mp3
```

响应为音频二进制，头信息：

```http
HTTP/1.1 200 OK
Content-Type: audio/mpeg
X-Request-ID: 5f1c0a9e2b7d4c31
X-Adapter-Warning: model-defaulted
Cache-Control: no-store
Transfer-Encoding: chunked
```

不主动设置 `Content-Length`：上游给的是 base64 长度，解码后长度不同。小于 2 KB 的响应由 `net/http` 自动补齐，较大响应走 chunked。

### 3.3 格式映射

| `response_format` | 上游 `audio.format` | 响应 `Content-Type` | 实测参数（2026-10-28） |
| --- | --- | --- | --- |
| `mp3`（默认） | `mp3` | `audio/mpeg` | MPEG2 Layer III，24 kHz VBR，带 Xing/Info 头 |
| `wav` | `wav` | `audio/wav` | 24 kHz / 16-bit / 单声道 |
| `pcm` | `pcm16` | `audio/pcm` | 24 kHz / 16-bit 小端 / 单声道 |
| `opus` / `aac` / `flac` | — | — | — 400 `unsupported_response_format` |

### 3.4 模型与音色

| 下游 `model` | 上游 `model` | 用途 |
| --- | --- | --- |
| 空 / `tts-1` / `tts-1-hd` / `gpt-4o-mini-tts` | `mimo-v2.5-tts` | 内置音色合成 |
| `mimo-v2.5-tts` | 原样 | 同上 |
| `mimo-v2.5-tts-voicedesign` | 原样 | 用 `instructions` 描述音色 |
| `mimo-v2.5-tts-voiceclone` | 原样 | 用 `voice` 传样本 |
| 其它 | — | 400 `model_not_found` |

内置音色别名（仅做存在性兼容，音色语义**不与** OpenAI 对齐）：

| OpenAI 音色 | MiMo 音色 |
| --- | --- |
| `alloy` | `冰糖` |
| `echo` | `苏打` |
| `fable` | `Mia` |
| `onyx` | `白桦` |
| `nova` | `茉莉` |
| `shimmer` | `Chloe` |
| `ash` | `Milo` |
| `ballad` | `Dean` |
| `coral` | `冰糖` |
| `sage` | `茉莉` |
| `verse` | `苏打` |

直接使用原生音色 ID 时原样下发：`mimo_default`、`冰糖`、`茉莉`、`苏打`、`白桦`、`Mia`、`Chloe`、`Milo`、`Dean`。
其中 `mimo_default` 的实际音色随部署集群而变（中国集群默认「冰糖」，其他集群默认 `Mia`），不要把它当作固定音色。

### 3.5 三种合成模式

**普通合成**（`mimo-v2.5-tts`）

```json
{
  "model": "tts-1",
  "voice": "nova",
  "input": "今天天气不错。",
  "instructions": "语气轻松。"
}
```

上行：

```json
{
  "model": "mimo-v2.5-tts",
  "messages": [
    { "role": "user", "content": "语气轻松。" },
    { "role": "assistant", "content": "今天天气不错。" }
  ],
  "audio": { "format": "mp3", "voice": "茉莉" },
  "stream": false
}
```

`instructions` 缺失时省略 `user` 消息；`input` 永远映射为 `assistant` 消息。

**音色设计**（`mimo-v2.5-tts-voicedesign`）

```json
{
  "model": "mimo-v2.5-tts-voicedesign",
  "input": "欢迎收听今天的节目。",
  "instructions": "中年男性播音员，声音低沉、语速平稳，略带磁性。",
  "response_format": "wav"
}
```

- `instructions` 必填（上游要求 `user` 消息），缺失返回 400 `missing_instructions`
- 传 `voice` 返回 400 `unsupported_parameter`（上游不接受该字段）

**音色克隆**（`mimo-v2.5-tts-voiceclone`）

```json
{
  "model": "mimo-v2.5-tts-voiceclone",
  "voice": "data:audio/wav;base64,UklGRiQAAABXQVZF...",
  "input": "这是用克隆音色合成的句子。"
}
```

- `voice` 必填，内容为样本音频的 base64（`mp3` / `wav`），可带 `data:` 前缀
- **上游要求 `audio.voice` 必须是 DataURL**：纯 base64 会被拒为
  `audio.voice must be a DataURL for voice clone model`。因此适配层统一**补全**
  `data:{mime};base64,` 前缀，而不是剥离它；MIME 按解码后的魔数判定，不信任声明值
- 样本上限 10 MB（base64 编码后）；格式仅 mp3 / wav
- 传 `instructions` 时忽略并回 `instructions-ignored`
- `user` 消息不影响克隆结果，适配层不额外插入

### 3.6 上行映射总表

| 下游 | 上游 | 规则 |
| --- | --- | --- |
| `input` | `messages[assistant].content` | 必填，长度校验在本地完成 |
| `instructions` | `messages[user].content` | 普通合成可省略；音色设计必填 |
| `voice` | `audio.voice` | 别名解析后下发；克隆模型下发样本 base64 |
| `response_format` | `audio.format` | 见 3.3；**任何情况都显式下发** |
| `speed` | — | 忽略，回警告 |
| `model` | `model` | 见 3.4 |
| — | `stream` | M2 固定 `false` |

## 4. 辅助端点

### 4.1 `GET /v1/models`

只列**已实现**的模型，别名可接受但不列出：

```json
{
  "object": "list",
  "data": [
    { "id": "mimo-v2.5-asr", "object": "model", "created": 0, "owned_by": "xiaomi" },
    { "id": "mimo-v2.5-tts", "object": "model", "created": 0, "owned_by": "xiaomi" },
    { "id": "mimo-v2.5-tts-voicedesign", "object": "model", "created": 0, "owned_by": "xiaomi" },
    { "id": "mimo-v2.5-tts-voiceclone", "object": "model", "created": 0, "owned_by": "xiaomi" }
  ]
}
```

不列出会返回 404 的模型；流式能力复用同一批 ID，不新增模型。

`created` 固定 `0`：这些模型没有可靠的发布时间，不编造。

### 4.2 `GET /healthz`

```json
{ "status": "ok", "version": "v0.1.0", "commit": "a1b2c3d" }
```

不检查上游连通性：健康检查只反映本进程状态，避免上游抖动导致容器被反复重启。上游可用性由实际请求的错误码体现。

## 5. 错误

### 5.1 状态码与错误码

| HTTP | `type` | `code` | 触发条件 | 客户端应对 |
| --- | --- | --- | --- | --- |
| 400 | `invalid_request_error` | `invalid_json` | body 不是合法 JSON | 修请求 |
| 400 | `invalid_request_error` | `invalid_multipart` | multipart 解析失败 | 修请求 |
| 400 | `invalid_request_error` | `missing_file` | 没有 `file` 字段 | 补字段 |
| 400 | `invalid_request_error` | `empty_file` | 音频为 0 字节 | 修请求 |
| 400 | `invalid_request_error` | `unsupported_audio_format` | 魔数不是 mp3/wav | 转码后重试 |
| 400 | `invalid_request_error` | `missing_input` | `input` 为空 | 补字段 |
| 400 | `invalid_request_error` | `text_too_long` | 超过 `MAX_TTS_CHARS` | 拆分文本 |
| 400 | `invalid_request_error` | `missing_instructions` | 音色设计缺 `instructions` | 补字段 |
| 400 | `invalid_request_error` | `unsupported_response_format` | 请求 srt/vtt/opus/aac/flac | 换格式 |
| 400 | `invalid_request_error` | `unsupported_parameter` | 参数组合非法（如 voicedesign + voice、M2 的 `sse`、M1 的 `stream=true`） | 去掉该参数 |
| 400 | `invalid_request_error` | `model_not_found` | 未知模型 | 换模型 |
| 400 | `invalid_request_error` | `voice_not_found` | 未知音色 | 换音色 |
| 400 | `invalid_request_error` | `missing_voice` | 克隆模型未提供 `voice` | 补字段 |
| 401 | `authentication_error` | `unauthorized` | 缺 token | 补 `Authorization` |
| 401 | `authentication_error` | `invalid_token` | token 不匹配 | 检查 token |
| 404 | `invalid_request_error` | `not_found` | 路由不存在 | 检查路径 |
| 405 | `invalid_request_error` | `method_not_allowed` | 方法不对 | 换方法 |
| 413 | `invalid_request_error` | `payload_too_large` | 超过 `MAX_UPLOAD_BYTES` | 压缩或分段 |
| 429 | `rate_limit_error` | `rate_limited` | 上游 429 | 按 `Retry-After` 退避 |
| 500 | `api_error` | `internal_error` | 未捕获异常 | 反馈并附 `X-Request-ID` |
| 502 | `api_error` | `upstream_invalid_request` | 上游 400/422 | 反馈，属于映射缺陷 |
| 502 | `api_error` | `upstream_auth_error` | 上游 401/403 | 运维检查 `MIMO_API_KEY` |
| 502 | `api_error` | `upstream_unreachable` | 连接失败 / DNS 失败 | 稍后重试 |
| 502 | `api_error` | `upstream_bad_response` | 上游 200 但结构不可解析，或上游 404（`MIMO_BASE_URL` 配错） | 反馈；404 时检查上游地址 |
| 502 | `api_error` | `upstream_truncated` | `finish_reason=length` | 拆分输入后重试 |
| 504 | `api_error` | `upstream_timeout` | 超过 `UPSTREAM_TIMEOUT` | 稍后重试或缩短输入 |

`upstream_invalid_request` 用 502 而不是 400：请求是我们翻译后发出去的，上游拒绝说明映射有问题，不是客户端写错了。

### 5.2 约束

- 上游 `message` 截断到 512 字符，保留原文便于排查
- 不透传上游 `WWW-Authenticate`
- 401 响应带 `WWW-Authenticate: Bearer`
- 任何错误响应都不包含密钥、请求头、音频内容

## 6. 与 OpenAI 的行为差异

| 差异 | 影响 | 处理 |
| --- | --- | --- |
| 无时间戳 | `srt` / `vtt` 不可用，`verbose_json` 无 `segments` | 明确 400，不伪造时间轴 |
| 无词级时间戳 | `timestamp_granularities[]=word` 不可用 | 400 |
| `language` 仅 `auto` / `zh` / `en` | 请求其它语言会被降级 | 回落 `auto` + `language-fallback-auto` |
| `prompt` 不生效 | 无法用提示词引导专有名词 | 接受但忽略并警告 |
| `temperature` 不生效 | 无法调随机性 | 接受但忽略并警告 |
| `speed` 不生效 | 语速不可控 | 忽略并警告；需要变速请自行后处理 |
| 音色语义不对齐 | `nova` 不等于 OpenAI 的 nova | 别名仅保证「存在且可用」 |
| 不支持输入 `webm` / `m4a` / `ogg` / `flac` | 浏览器直录的 `webm` 需先转码 | 400 + 明确提示 |
| 不支持输出 `opus` / `aac` / `flac` | 部分客户端默认要 opus | 400 + 明确提示 |
| 错误文案可能为中文 | 客户端日志可读性 | `code` 已归一化，按 `code` 判断 |
| `/v1/models` 不含别名 | 下拉框只显示原生模型 | 别名仍可用 |

## 7. 上游契约快照

核对日期 2026-10-28，来源：

- <https://mimo.mi.com/docs/en-US/api/audio/Speech-Recognition>
- <https://mimo.mi.com/docs/en-US/api/audio/tts>

### 7.1 识别

请求：

```json
{
  "model": "mimo-v2.5-asr",
  "messages": [
    {
      "role": "user",
      "content": [
        {
          "type": "input_audio",
          "input_audio": { "data": "data:audio/wav;base64,..." }
        }
      ]
    }
  ],
  "asr_options": { "language": "auto" }
}
```

响应关键字段：

```json
{
  "choices": [{ "message": { "role": "assistant", "content": "转写文本" } }],
  "usage": { "seconds": 3, "prompt_tokens_details": { "audio_tokens": 42 } }
}
```

### 7.2 合成

请求：

```json
{
  "model": "mimo-v2.5-tts",
  "messages": [
    { "role": "user", "content": "风格提示（可选）" },
    { "role": "assistant", "content": "待合成文本" }
  ],
  "audio": { "format": "wav", "voice": "mimo_default" }
}
```

响应关键字段：

```json
{
  "choices": [
    {
      "message": {
        "audio": {
          "id": "979a91904f9a4143928d9e1f54837b4f",
          "data": "base64Data",
          "expires_at": null,
          "transcript": null
        }
      }
    }
  ]
}
```

流式（`stream=true`）时音频在 `choices[0].delta.audio.data`，每片都是 base64 片段；`audio.format` 不传时默认 `pcm`。

## 8. 实测结论

2026-10-28 用官方 key 对 `https://api.xiaomimimo.com/v1` 完成首轮联调（本地可执行文件 + 官方端点）。

### 8.1 逐项结论

| 编号 | 项目 | 结论 | 处理 |
| --- | --- | --- | --- |
| Q1 | 克隆样本传参 | **必须是 DataURL**。纯 base64 返回 400 `audio.voice must be a DataURL for voice clone model`；带 `data:audio/wav;base64,` 前缀即 200 | 已修：适配层由「剥离前缀」改为「补全前缀」 |
| Q2 | `pcm16` 参数 | 24000 Hz / 16-bit / 单声道 / 小端，与 wav 一致 | 已写入 3.3 |
| Q3 | 文本上限 | 760 字可正常合成（耗时 74.7 s，输出 1.9 MB mp3），未触及上限 | `MAX_TTS_CHARS` 保守保留 |
| Q4 | ASR 时长上限 | 未测（仅验证 2.56 s 样本） | M3 前补 |
| Q5 | 流式分片独立解码 | 未测 | M3 必测 |
| Q6 | 并发与 429 | 未测 | M3 |
| Q7 | `finish_reason=length` 触发条件 | 未测 | M3 |

### 8.2 其他实测事实

- **`usage.seconds` 是整数秒**（2.56 s 音频返回 3），`verbose_json` 的 `duration` 直接透传该值，因此也是整数，与 OpenAI 的浮点 `duration` 有差异。
- **非 TTS 模型的 `message` 带 `"audio": null`**，TTS 模型的 `message.content` 是空字符串 `""`；两者都能被现有结构正确解析。
- **mp3 是 MPEG2 Layer III**（不是 MPEG1），24 kHz，带 Xing/Info 头，帧长按 `72*bitrate/48000` 计算；用 MPEG1 的 1152 样本/帧去解析会误判为损坏文件。
- **生成耗时随文本线性增长**：约 20 字 ≈ 2 s，760 字 ≈ 75 s。默认 180 s 超时对超长文本可能偏紧，调大 `MIMO_TIMEOUT` 可缓解。
- **`instructions` 对普通 TTS 生效**：作为 `user` 消息下发后，未出现 `instructions-ignored` 警告。
- **`user` 空消息不是克隆的必需条件**：不带 `user` 消息的克隆请求同样 200。

### 8.3 已知不兼容的上游

`sub2api` 类中转网关（仅做 OpenAI chat 响应归一化）**不能**承载本适配器：

- `input_audio` content part 被静默丢弃，音频根本没到上游；
- `message.audio` / `delta.audio` 在响应转换时被剥离，返回 200 但没有音频；
- 网关自身的 `/v1/audio/*` 不存在（404）。

适配器对这类上游会明确报错（见 6 节）：ASR 得到 `upstream_error`，TTS 得到 `upstream_bad_response`，不会返回空音频文件。
