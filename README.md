# MiMoVoice2API

[![CI](https://github.com/mcxiaochenn/MiMoVoice2API/actions/workflows/ci.yml/badge.svg)](https://github.com/mcxiaochenn/MiMoVoice2API/actions/workflows/ci.yml)

把小米 MiMo 开放平台的语音识别与语音合成，翻译成 OpenAI 兼容的 `/v1/audio/transcriptions` 与 `/v1/audio/speech`。单个 Go 二进制，Docker 部署。

> **当前状态：M4（转写、合成、流式、指标与镜像发布）**。转写与合成（含 SSE / chunked 流式）、模型列表、健康检查、Prometheus 指标均已实现；tag `v*` 触发 GHCR 多架构镜像发布与二进制附件。
> 接口契约见 [docs/api-spec.md](docs/api-spec.md)，实施节奏见 [docs/roadmap.md](docs/roadmap.md)。

## 为什么需要它

MiMo 的语音能力只暴露在 `POST /v1/chat/completions` 上：

| 能力 | 模型 | 上游形态 |
| --- | --- | --- |
| 语音识别 | `mimo-v2.5-asr` | 音频以 base64 data URL 塞进 `messages[].content[].input_audio`，转写结果取 `choices[0].message.content` |
| 语音合成 | `mimo-v2.5-tts` / `-voicedesign` / `-voiceclone` | 待合成文本放在 `assistant` 消息，音频 base64 在 `choices[0].message.audio.data` |

而现成生态（Open WebUI、LibreChat、Home Assistant、各家 OpenAI SDK 封装）默认只会调 `/v1/audio/*`。这个项目就是中间那层薄适配：**只做协议翻译，不做模型、不做转码、不落盘**。

## 端点

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/v1/audio/transcriptions` | 语音转写，`multipart/form-data`；支持 `stream=true` SSE |
| POST | `/v1/audio/speech` | 语音合成，JSON 进、音频流出；`stream_format=audio` / `sse` |
| GET | `/v1/models` | 可用模型列表 |
| GET | `/healthz` | 健康检查（含版本号），免鉴权 |
| GET | `/metrics` | Prometheus 指标，需要鉴权 |

客户端 `base_url` 填 `http://<host>:8080/v1`。

## 能力矩阵

| OpenAI 参数 / 能力 | 支持度 | 说明 |
| --- | --- | --- |
| 转写 `response_format`: `json` / `text` / `verbose_json` | 支持 | `verbose_json` 不含 `segments` |
| 转写 `response_format`: `srt` / `vtt` | 不支持 | 上游不给时间戳，直接 400 |
| 输入音频 `mp3` / `wav` | 支持 | 上游仅此两种，按文件魔数嗅探，不看扩展名 |
| 输入音频 `webm` / `m4a` / `ogg` / `flac` | 不支持 | 不内置 ffmpeg，明确 400 而不是静默出错 |
| 输出音频 `mp3` / `wav` / `pcm` | 支持 | |
| 输出音频 `opus` / `aac` / `flac` | 不支持 | 同上 |
| `instructions`（风格提示） | 支持 | 映射为 MiMo 的 `user` 消息 |
| `speed` | 忽略 | 上游无此参数；非 1.0 时回 `X-Adapter-Warning` |
| `prompt` / `temperature` | 忽略 | 上游无对应能力 |
| 流式转写 `stream=true` | 支持 | SSE：`transcript.text.delta` / `.done`；与 `verbose_json` 互斥 |
| 流式合成 `stream_format` | 支持 | `audio`（chunked 二进制）或 `sse`（`speech.audio.delta` / `.done`） |
| 流式 `wav` | 降级 | 上游分片是独立 WAV 文件，只能整段下发；要低延迟用 `mp3` / `pcm` |
| 音色克隆 / 音色设计 | 支持 | 通过 `model` + `voice` / `instructions` 触发 |

输出音频参数（2026-10-28 官方端点实测）：`mp3` 为 MPEG2 Layer III / 24 kHz VBR，带 Xing 头；`wav` 与 `pcm` 均为 24 kHz / 16-bit / 单声道。`pcm` 没有文件头，消费端需按这组参数解释。

## 快速开始

镜像已发布到 GHCR（`linux/amd64` 与 `linux/arm64`，tag `1.0.0` / `1.0` / `latest`，可匿名拉取）：

```bash
docker run -d --name mimivoice2api \
  -p 127.0.0.1:8080:8080 \
  -e MIMO_API_KEY=sk-your-mimo-key \
  -e AUTH_TOKEN=local-dev-token \
  --read-only --tmpfs /tmp --cap-drop ALL \
  --security-opt no-new-privileges \
  ghcr.io/mcxiaochenn/mimivoice2api:latest
```

或者用 [docker-compose.yml](docker-compose.yml)（已包含只读根、cap_drop、非 root、日志轮转）：

```bash
cp .env.example .env   # 填入 MIMO_API_KEY 与 AUTH_TOKEN
docker compose up -d
```

从源码构建：

```bash
docker build -t mimivoice2api:dev .
docker run --rm -p 8080:8080 --env-file .env mimivoice2api:dev
```

OpenAI Python SDK 接入：

```python
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:8080/v1", api_key="local-dev-token")

with open("sample.wav", "rb") as f:
    print(client.audio.transcriptions.create(model="whisper-1", file=f).text)

speech = client.audio.speech.create(model="tts-1", voice="nova", input="你好，这是一段测试。")
speech.write_to_file("out.mp3")
```

流式：转写逐字回显，合成边收边播。

```python
# 转写：SSE 增量文本
with open("sample.wav", "rb") as f:
    for event in client.audio.transcriptions.create(model="whisper-1", file=f, stream=True):
        if event.type == "transcript.text.delta":
            print(event.delta, end="", flush=True)
        elif event.type == "transcript.text.done":
            print("\n最终:", event.text)
```

```bash
# 合成：chunked 二进制，边收边写
curl -sS -N localhost:8080/v1/audio/speech \
  -H "Authorization: Bearer $AUTH_TOKEN" -H "Content-Type: application/json" \
  -d '{"voice":"nova","input":"流式合成。","response_format":"pcm"}' \
  --output out.pcm

# 合成：SSE 事件
curl -sS -N localhost:8080/v1/audio/speech \
  -H "Authorization: Bearer $AUTH_TOKEN" -H "Content-Type: application/json" \
  -d '{"voice":"nova","input":"流式合成。","stream_format":"sse"}'
```

## 配置

全部通过环境变量，启动时校验，缺关键项直接退出。样例见 [.env.example](.env.example)。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MIMO_API_KEY` | 无（必填） | 上游 API Key，只从环境变量读 |
| `MIMO_BASE_URL` | `https://api.xiaomimimo.com/v1` | 上游地址，必须以 `/v1` 收尾（允许路径前缀），便于指向代理或测试桩；拒绝 query / fragment / userinfo |
| `MIMO_ASR_MODEL` | `mimo-v2.5-asr` | 请求未指定模型时的转写默认值 |
| `MIMO_TTS_MODEL` | `mimo-v2.5-tts` | 请求未指定模型时的合成默认值 |
| `LISTEN_ADDR` | `:8080` | 监听地址 |
| `AUTH_TOKEN` | 空 | 下游 Bearer token；留空表示不校验，**且只允许回环监听**（见下方说明） |
| `MAX_UPLOAD_BYTES` | `26214400`（25 MiB） | 上传音频上限，超限 413 |
| `MAX_TTS_CHARS` | `1000` | 单次合成文本字符上限 |
| `MAX_CONCURRENT_REQUESTS` | `4` | 并发处理上限，超出 503 `server_busy`；`/healthz` 不占额度 |
| `UPSTREAM_TIMEOUT` | `180s` | 单次上游请求总超时 |
| `METRICS_ENABLED` | `true` | 是否暴露 `/metrics`；关闭后该路径返回 404 |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `LOG_FORMAT` | `json` | `json` / `text` |

> **行为变化（稳定性修复后）**：`AUTH_TOKEN` 为空时启动会强制 `LISTEN_ADDR` 为回环地址
> （`127.0.0.0/8`、`::1`、`localhost`）；配置了 `:8080` / `0.0.0.0` 等非回环监听会**直接拒绝启动**，
> 避免无鉴权服务被无意暴露到公网。本机匿名调试请显式设置 `LISTEN_ADDR=127.0.0.1:8080`。

## 可观测性

结构化日志默认 JSON，固定字段：`request_id`、`route`、`status`、`dur_ms`、`in_bytes`、`out_bytes`、`model_requested`、`model_upstream`、`upstream_status`、`err_code`。不记录音频、转写文本、合成文本与密钥。

`/metrics` 输出 Prometheus 文本格式，与业务端点一样需要 Bearer token：

```bash
curl -sS localhost:8080/metrics -H "Authorization: Bearer $AUTH_TOKEN" | head -20
```

| 指标 | 用途 |
| --- | --- |
| `mimo_voice2api_http_requests_total{route,status,err_code}` | 请求量与错误率，成功时 `err_code="none"` |
| `mimo_voice2api_http_request_duration_seconds{route}` | 端到端时延直方图 |
| `mimo_voice2api_upstream_requests_total{kind,status}` | 上游错误分类；429 重试会计两次 |
| `mimo_voice2api_upstream_latency_seconds{kind}` | 上游到响应头的时延 |
| `mimo_voice2api_http_in_flight_requests` | 在途请求数 |
| `mimo_voice2api_go_goroutines` / `..._go_heap_alloc_bytes` | 泄漏排查 |

完整指标列表、抓取配置与 PromQL 示例见 [docs/api-spec.md](docs/api-spec.md#43-get-metrics)。

## 故障排查

| 现象 | 可能原因 | 处理 |
| --- | --- | --- |
| `401 unauthorized` | 请求没带 `Authorization` 头 | 补 `Bearer <AUTH_TOKEN>` |
| `401 invalid_token` | 客户端 token 与 `AUTH_TOKEN` 不一致 | 对齐两边，注意复制时的空格与换行 |
| `413 payload_too_large` | 上传超过 `MAX_UPLOAD_BYTES`（默认 25 MiB） | 调大限制或先压缩音频；multipart 边界也计入总量 |
| `400 unsupported_audio_format` | 传了 webm / m4a / ogg / flac，或扩展名伪装 | 先用 ffmpeg 转 mp3 / wav，本服务不做转码 |
| `429 rate_limited` | 上游限流，且没有可用的 `Retry-After` 或等待超过 10s | 降低并发；能重试时响应头会带 `Retry-After` |
| `503 server_busy` | 并发达到 `MAX_CONCURRENT_REQUESTS`（默认 4） | 稍后重试，或调大该值 / 降低客户端并发 |
| `502 upstream_auth_error` | `MIMO_API_KEY` 无效或过期 | 换 key；这里故意不用 401，避免客户端误清自己的 token |
| `502 upstream_bad_response` | 上游 200 但没有音频 / choices，或 JSON 不可解析 | 常见于把本项目接到会丢弃 `audio` 字段的中转网关，换成官方端点 |
| `504 upstream_timeout` | 超过 `UPSTREAM_TIMEOUT` | 缩短文本或调大超时；流式响应不受写超时限制 |
| 容器反复重启 | `/healthz` 不通 | 看启动日志，多为 `MIMO_API_KEY` 缺失或地址格式错误 |

排查顺序：先看日志里的 `request_id` 与 `err_code`，再用同一个 `request_id` 对照上游响应；`/metrics` 的 `upstream_requests_total{kind=...}` 能直接区分本地校验失败与上游失败。

## 接口映射速查

完整规则、错误码与降级行为见 [docs/api-spec.md](docs/api-spec.md)。

| 下游（OpenAI） | 上游（MiMo `/chat/completions`） |
| --- | --- |
| `file`（multipart） | `messages[0].content[0].input_audio.data` = `data:audio/wav;base64,...` |
| `language` | `asr_options.language`，仅 `auto` / `zh` / `en` |
| `input`（speech） | `messages[assistant].content` |
| `instructions` | `messages[user].content` |
| `voice` | `audio.voice` |
| `response_format` | `audio.format`，仅 `mp3` / `wav` / `pcm` |
| `model` 别名 | `whisper-1` 等 → `mimo-v2.5-asr`；`tts-1` 等 → `mimo-v2.5-tts` |

## 文档

| 文档 | 内容 |
| --- | --- |
| [docs/architecture.md](docs/architecture.md) | 组件划分、请求流水线、内存模型、超时与部署形态、已知坑位 |
| [docs/api-spec.md](docs/api-spec.md) | 端点契约、字段映射、错误码、与 OpenAI 的行为差异、上游契约快照 |
| [docs/conventions.md](docs/conventions.md) | 目录约定、分层规则、命名、日志字段、测试与 Git 规范 |
| [docs/roadmap.md](docs/roadmap.md) | M0–M4 里程碑、验收标准、风险与未决问题 |
| [AGENTS.md](AGENTS.md) | 给 AI 代理与贡献者的硬性约束和操作流程 |

## 开发

本地运行（Docker 之外不会自动读 `.env`，直接给环境变量）：

```bash
MIMO_API_KEY=sk-your-key AUTH_TOKEN=dev-token go run ./cmd/mimivoice2api

go vet ./...
go test ./...
gofmt -l ./cmd ./internal

curl -sS localhost:8080/metrics -H "Authorization: Bearer dev-token" | head
```

`go test -race` 需要 cgo 与 gcc，本机缺编译器时由 CI 执行。CI 还会检查 `gofmt`、跑 `vet`，并用 buildx 验证 `linux/amd64` + `linux/arm64` 都能构建；推送 `v*` tag 才会真正发镜像。

验收以本地 `go build` 产物 + curl 冒烟为准，不要求 Docker 实装测试。也可以先 `go build -o bin/mimivoice2api ./cmd/mimivoice2api` 再直接运行。

本机没有 `MIMO_API_KEY` 时，测试全程使用 `httptest` 假上游，不访问真实网络。

## 目录（目标态）

```text
cmd/mimivoice2api/    入口，只做装配
internal/config/      环境变量 → Config
internal/httpapi/     路由、中间件、handler、错误 envelope
internal/adapter/     纯映射函数与别名表，不认识 HTTP
internal/mimo/        上游客户端、SSE 解析与协议类型
internal/metrics/     零依赖 Prometheus 文本格式实现
internal/audio/       魔数嗅探与 MIME 映射
docs/                 设计与契约
```

细节见 [docs/conventions.md](docs/conventions.md)。

## 非目标

- 不做音频转码、重采样、格式互转（需要就自行在前置环节用 ffmpeg）
- 不做通用 LLM 代理，只翻译 audio 两个端点
- 不做多租户、计费、配额、后台管理
- 不做缓存、队列、异步任务、结果回查
- 不持久化任何音频字节或转写文本
- 不提供 Web UI

## 许可

未定。在明确许可证之前，默认保留所有权利。
