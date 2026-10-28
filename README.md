# MiMoVoice2API

把小米 MiMo 开放平台的语音识别与语音合成，翻译成 OpenAI 兼容的 `/v1/audio/transcriptions` 与 `/v1/audio/speech`。单个 Go 二进制，Docker 部署。

> **当前状态：M1（转写可用）**。已实现非流式语音转写、模型列表与健康检查，附带 Dockerfile / Compose；语音合成（M2）、流式（M3）与 GHCR 镜像发布（M4）尚未完成。
> 接口契约见 [docs/api-spec.md](docs/api-spec.md)，实施节奏见 [docs/roadmap.md](docs/roadmap.md)。

## 为什么需要它

MiMo 的语音能力只暴露在 `POST /v1/chat/completions` 上：

| 能力 | 模型 | 上游形态 |
| --- | --- | --- |
| 语音识别 | `mimo-v2.5-asr` | 音频以 base64 data URL 塞进 `messages[].content[].input_audio`，转写结果取 `choices[0].message.content` |
| 语音合成 | `mimo-v2.5-tts` / `-voicedesign` / `-voiceclone` | 待合成文本放在 `assistant` 消息，音频 base64 在 `choices[0].message.audio.data` |

而现成生态（Open WebUI、LibreChat、Home Assistant、各家 OpenAI SDK 封装）默认只会调 `/v1/audio/*`。这个项目就是中间那层薄适配：**只做协议翻译，不做模型、不做转码、不落盘**。

## 端点

| 方法 | 路径 | 说明 | 里程碑 |
| --- | --- | --- | --- |
| POST | `/v1/audio/transcriptions` | 语音转写，`multipart/form-data` | M1 非流式 / M3 流式 |
| POST | `/v1/audio/speech` | 语音合成，JSON 进、音频流出 | M2 非流式 / M3 流式 |
| GET | `/v1/models` | 可用模型列表 | M1 |
| GET | `/healthz` | 健康检查（含版本号） | M1 |

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
| 流式（`stream` / `stream_format`） | M3 | M1/M2 明确拒绝 |
| 音色克隆 / 音色设计 | M2 | 通过 `model` + `voice` / `instructions` 触发 |

## 快速开始

以下命令在 M1 之后可用，当前无法执行：

```bash
docker run --rm -p 8080:8080 \
  -e MIMO_API_KEY=sk-your-mimo-key \
  -e AUTH_TOKEN=local-dev-token \
  ghcr.io/mcxiaochenn/mimivoice2api:latest
```

镜像会在 M4 发布到 GHCR；在那之前用源码构建：

```bash
cp .env.example .env   # 填入 MIMO_API_KEY 与 AUTH_TOKEN
docker build -t mimivoice2api:dev .
docker run --rm -p 8080:8080 --env-file .env mimivoice2api:dev
```

OpenAI Python SDK 接入（M1 起转写可用，M2 起合成可用）：

```python
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:8080/v1", api_key="local-dev-token")

with open("sample.wav", "rb") as f:
    print(client.audio.transcriptions.create(model="whisper-1", file=f).text)

speech = client.audio.speech.create(model="tts-1", voice="nova", input="你好，这是一段测试。")
speech.write_to_file("out.mp3")
```

## 配置

全部通过环境变量，启动时校验，缺关键项直接退出。样例见 [.env.example](.env.example)。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MIMO_API_KEY` | 无（必填） | 上游 API Key，只从环境变量读 |
| `MIMO_BASE_URL` | `https://api.xiaomimimo.com/v1` | 上游地址，必须包含 `/v1`，便于指向代理或测试桩 |
| `MIMO_ASR_MODEL` | `mimo-v2.5-asr` | 请求未指定模型时的转写默认值 |
| `MIMO_TTS_MODEL` | `mimo-v2.5-tts` | 请求未指定模型时的合成默认值 |
| `LISTEN_ADDR` | `:8080` | 监听地址 |
| `AUTH_TOKEN` | 空 | 下游 Bearer token；留空表示不校验，仅限本机调试 |
| `MAX_UPLOAD_BYTES` | `26214400`（25 MiB） | 上传音频上限，超限 413 |
| `MAX_TTS_CHARS` | `1000` | 单次合成文本字符上限 |
| `UPSTREAM_TIMEOUT` | `180s` | 单次上游请求总超时 |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `LOG_FORMAT` | `json` | `json` / `text` |

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
gofmt -l .
```

`go test -race` 需要 cgo 与 gcc，本机缺编译器时由 CI 执行。

验收以本地 `go build` 产物 + curl 冒烟为准，不要求 Docker 实装测试。也可以先 `go build -o bin/mimivoice2api ./cmd/mimivoice2api` 再直接运行。

本机没有 `MIMO_API_KEY` 时，测试全程使用 `httptest` 假上游，不访问真实网络。

## 目录（目标态）

```text
cmd/mimivoice2api/    入口，只做装配
internal/config/      环境变量 → Config
internal/httpapi/     路由、中间件、handler、错误 envelope
internal/adapter/     纯映射函数与别名表，不认识 HTTP
internal/mimo/        上游客户端与协议类型
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
