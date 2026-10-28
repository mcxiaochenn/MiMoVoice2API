# 里程碑

原则：每个里程碑都能独立验证，不堆未验证的改动。复杂度用 S / M / L 表示，不用工期承诺。

**验收约定**（2026-10-28）：以本地 `go build` 产物 + curl 冒烟为准；不要求 Docker 实装测试，也不强制真实上游 key。Dockerfile 仍作为交付物维护，但不构成验收门槛。

## M0 — 仓库与文档规划（已完成）

**目标**：把契约、架构、约定、节奏写清楚，让后续实现不需要再做设计决策。

| 交付物 | 状态 |
| --- | --- |
| Git 仓库（`main` 分支，首个 commit 只含文档） | 本轮完成 |
| `README.md`：定位、端点、能力矩阵、配置 | 本轮完成 |
| `AGENTS.md`：硬性约束、分层、DoD、提交规范 | 本轮完成 |
| `docs/architecture.md`：组件、流水线、内存模型、坑位 | 本轮完成 |
| `docs/api-spec.md`：字段映射、错误码、行为差异、上游快照 | 本轮完成 |
| `docs/conventions.md`：目录、命名、日志、测试、Git | 本轮完成 |
| `docs/roadmap.md`：本文件 | 本轮完成 |

**验收**

- [x] `git log --oneline` 只有文档相关提交
- [x] `git ls-files '*.go' 'go.mod' 'Dockerfile'` 为空
- [x] 文档之间的相对链接全部可达
- [x] api-spec 覆盖 ASR/TTS 的每一个 OpenAI 字段，且每个「不支持」都有明确错误表现

**不做**：任何 Go 代码、Dockerfile、CI 配置。

## M1 — 骨架与转写（已实现，待真实环境验收）

**目标**：`/v1/audio/transcriptions` 非流式端到端可用，镜像能跑起来。

**状态**：代码、单元/契约测试与 Docker 文件已完成；已用本地可执行文件对假上游完成成功链路（`json` / `text` / `verbose_json`）与失败链路（不可达、404、格式拒绝）冒烟。真实上游 key 与 Docker 实装不作为验收门槛。

范围：

- `go.mod`（Go 1.26，零第三方依赖）、`cmd/mimivoice2api/main.go`
- `internal/config`：11 个环境变量、启动校验、`AUTH_TOKEN` 为空时 WARN
- `internal/httpapi`：路由、request id、recover、Bearer 鉴权、`MaxBytesReader`、访问日志、错误 envelope
- `internal/audio/sniff.go`：mp3 / wav 魔数判定
- `internal/adapter/asr.go` + `model.go`：字段映射、模型别名、language 归一化
- `internal/mimo/client.go` + `types.go`：`POST /chat/completions`，状态码分类
- 端点：`/healthz`、`/v1/models`、`/v1/audio/transcriptions`（`json` / `text` / `verbose_json`）
- `Dockerfile`、`.dockerignore`、`docker-compose.yml`
- 测试：adapter 表驱动、handler 假上游、错误码断言

**验收**

```bash
go build ./... && go vet ./... && go test ./...

# 可选：Docker 打包检查，不参与验收
docker build -t mimivoice2api:dev .
docker run --rm -d --name m2a -p 8080:8080 --env-file .env mimivoice2api:dev
curl -sS localhost:8080/healthz
curl -sS localhost:8080/v1/audio/transcriptions \
  -H "Authorization: Bearer $AUTH_TOKEN" \
  -F file=@testdata/audio/sample-1s.wav -F model=whisper-1
```

- [x] 本地可执行文件 + 假上游返回非空 `text`
- [x] `response_format=text` 返回纯文本且无 JSON 包装
- [x] `verbose_json` 的 `duration` 取自上游 `usage.seconds`
- [x] 上传 `webm` 返回 400 `unsupported_audio_format`
- [x] 未知模型返回 400 `model_not_found`
- [x] 无 token 返回 401，错误 token 返回 401 `invalid_token`
- [x] 日志中没有 base64、文本内容、密钥（冒烟日志确认）
- [x] 上游 404 / 不可达分别映射为 `upstream_bad_response` / `upstream_unreachable`
- [x] Dockerfile 与 compose 已提供（构建运行不在验收范围）

**风险**：上游 `input_audio` 对 data URL 的格式要求可能与文档有偏差 → 首个联调用例先打通最小请求，再补参数。

## M2 — 合成（已实现，本地可执行文件验收）

**目标**：`/v1/audio/speech` 非流式可用，覆盖普通合成、音色设计、音色克隆。

**状态**：代码与测试完成；本地可执行文件 + 假上游已跑通三种模型、三种输出格式与错误映射。真实上游的音频可播放性与克隆样本格式（Q1）仍需可选联调，不作为门槛。

范围：

- `internal/httpapi/speech.go`：JSON 解码、长度校验、`speed` / `stream_format` 处理
- `internal/adapter/tts.go` + `voice.go`：音色别名、克隆样本判定
- 二进制响应：`base64.NewDecoder` 流式解码，`Content-Type` 与格式映射
- 三种 MiMo TTS 模型的参数组合校验
- 响应头 `X-Adapter-Warning`
- 更新 `/v1/models`，加入三个 TTS 模型

**验收**

```bash
# 可选：真实上游联调，不作为验收门槛
curl -sS localhost:8080/v1/audio/speech \
  -H "Authorization: Bearer $AUTH_TOKEN" -H "Content-Type: application/json" \
  -d '{"model":"tts-1","voice":"nova","input":"测试一句话。","response_format":"mp3"}' \
  --output out.mp3
```

- [x] 本地可执行文件 + 假上游返回音频，字节与上游 base64 解码结果一致
- [x] `wav` / `pcm` 输出头与 `Content-Type` 一致（`audio/wav`、`audio/pcm`）
- [x] `voice=冰糖` 与 `voice=nova` 均映射到合法上游音色（请求体断言）
- [x] `voicedesign` 缺 `instructions` 返回 400 `missing_instructions`
- [x] `voicedesign` 传 `voice` 返回 400 `unsupported_parameter`
- [x] `speed=1.5` 返回音频且带 `X-Adapter-Warning: speed-ignored`
- [x] `response_format=opus` 返回 400
- [x] 克隆样本的 `data:` 前缀被剥离，以纯 base64 下发（请求体断言）
- [ ] 真实上游音频可播放、`ffprobe` 识别正常（可选）
- [ ] 回填 api-spec 第 8 节的 Q1（真实上游实测，可选）

**风险**：克隆样本的传参形式未在真实上游验证；已按文档统一为纯 base64，Q1 结论不阻塞 M3。

## M3 — 流式（复杂度 L）

**目标**：转写与合成的流式路径打通，首字节延迟显著低于非流式。

范围：

- `internal/mimo/sse.go`：SSE 解析，逐片回调
- 转写 `stream=true`：上游 `delta.content` → 下游 `transcript.text.delta` / `transcript.text.done`
- 合成 `stream_format=sse`：上游 `delta.audio.data` → 下游 `speech.audio.delta` / `speech.audio.done`
- 合成 `stream_format=audio`：chunked 二进制，边解 base64 边写
- 客户端断开 → 取消上游请求；`Flusher` 及时 flush
- 429 单次重试（尊重 `Retry-After`）

**验收**

- [ ] `curl -N` 能看到连续事件，事件顺序与终止事件正确
- [ ] 中断客户端后，上游连接在 1s 内关闭（看日志与上游用量）
- [ ] 流式合成不再整段缓冲，首字节时间 < 非流式的 50%（同一文本实测）
- [ ] 事件名与 OpenAI 官方文档一致（实现前复核，见 api-spec 待实测 Q5）

**风险**：base64 分片可能跨 chunk 断裂。若不能独立解码，需要维护解码缓冲，不能直接透传。

## M4 — 加固与发布（复杂度 M）

**目标**：可长期运行、可观测、可分发。

范围：

- `/metrics`：请求计数、时延直方图、上游错误分类
- 多阶段构建 + buildx 多架构（`linux/amd64`、`linux/arm64`）
- GitHub Actions：`vet` + `test -race` + 构建 + 推送 GHCR
- Compose 加固示例（只读根、cap_drop、非 root）
- README 增加真实部署示例与故障排查（401 / 413 / 429 三节）

**验收**

- [ ] 多架构镜像可拉取，`arm64` 上能转写
- [ ] CI 在 PR 上跑通，无网络访问真实上游
- [ ] 连续 24h 空跑内存稳定（无泄漏）
- [ ] 压测 10 并发 1 MiB 转写无 OOM，P95 时延有记录

## 依赖关系

```text
M0 ──→ M1 ──→ M2 ──→ M3 ──→ M4
        │      │
        └──────┴── 共用 adapter / mimo，M2 复用 M1 的客户端与错误分类
```

M3 依赖 M1/M2 的非流式路径作为回归基线，不单独起分支重写。

## 风险与未决问题

| 问题 | 影响 | 处理时机 |
| --- | --- | --- |
| 克隆样本格式（base64 vs data URL）未确认 | M2 克隆模式 | M2 首日实测 |
| `pcm16` 采样率未知 | 客户端播放异常 | M2 实测后写入 api-spec |
| 单次文本上限未公开 | 长文本可能被截断 | M2 实测，必要时下调 `MAX_TTS_CHARS` |
| 上游限流策略未知 | 高并发下 429 频发 | M3 压测 |
| MiMo 若推出原生 `/audio/*` | 本项目价值下降 | 保持薄适配层，届时评估直接反代 |
| 是否需要转码 | 浏览器 `webm` 直录不可用 | 明确不做；文档引导前置 ffmpeg |

## 版本节奏

| 里程碑 | tag | 说明 |
| --- | --- | --- |
| M1 | `v0.1.0` | 转写可用 |
| M2 | `v0.2.0` | 合成可用 |
| M3 | `v0.3.0` | 流式可用 |
| M4 | `v1.0.0` | 可长期运行，镜像发布 |
