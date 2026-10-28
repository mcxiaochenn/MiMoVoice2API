# AGENTS.md

给在本仓库工作的 AI 代理与人类贡献者的操作手册。**本文件的约束优先于个人习惯**；与用户当轮明确指令冲突时，以用户指令为准。

## 1. 项目是什么

无状态 HTTP 适配层：把 MiMo 的 `POST /v1/chat/completions`（音频模态）翻译成 OpenAI 风格的 `/v1/audio/transcriptions` 与 `/v1/audio/speech`。

- 语言与运行时：Go 1.26，`CGO_ENABLED=0`，只用标准库
- 部署：单二进制 + Docker（多阶段构建、非 root、只读根文件系统）
- 规模目标：核心实现约 3500 行（含注释，不含测试）；新增功能优先复用现有分层，不要靠加抽象扩张

## 2. 当前阶段闸门

**M0–M4 全部实现，项目进入维护态。**

- 新端点、新依赖、新配置项都属于范围变更：先改 [docs/api-spec.md](docs/api-spec.md)，再改代码
- 不要引入音频转码、缓存、队列、多租户，那超出项目边界
- 改流式逻辑时注意两条既有结论：分片可独立解码（无需跨片缓冲），但 `wav` 分片是各自独立的完整文件（必须整段下发）
- 改指标时保持标签基数受控：只允许固定枚举（route / status / err_code / kind），不要把用户输入当标签
- 发布：推送 `v*` tag 会自动构建双架构镜像、发 GitHub release 并附二进制；不要在本地手工构建发布物

## 3. 硬性约束（不要做）

| 禁止 | 原因 |
| --- | --- |
| 音频转码 / 重采样 / 格式互转 | 会把纯 Go 服务拖进 ffmpeg 依赖；不支持就返回 400 |
| Web 框架（gin / echo / chi）、ORM、DI 容器 | 标准库 `net/http` + `http.ServeMux` 足够 |
| 业务包直接读 `os.Getenv` | 配置只在 `internal/config` 解析一次，之后结构体注入 |
| 记录音频字节、base64、转写文本、API Key | 隐私与密钥泄漏 |
| 音频写磁盘、缓存、结果回查 | 无状态是核心卖点 |
| 把上游 401/403 原样透传给客户端 | 客户端会误以为自己的 token 错了，应映射为 502 |
| 为「以后可能用到」加抽象、开关、扩展点 | 只做当前里程碑要求的功能 |
| 在 handler 里写协议映射逻辑 | 映射必须是 `internal/adapter` 的纯函数，可脱网单测 |

## 4. 分层与依赖方向

```text
cmd/mimivoice2api  →  internal/httpapi  →  internal/adapter  →  internal/mimo
                          │  │                  │
                          │  └──→ internal/metrics
                          └─────→ internal/audio ←─┘
```

规则：

- `adapter` 不认识 `http.Request`、`http.ResponseWriter`，只接收自己的 Input 结构体
- `mimo` 只依赖标准库，不 import 其他内部包
- 禁止反向依赖，禁止循环依赖
- `audio` 只提供格式枚举、魔数嗅探、MIME 映射，不含业务语义

## 5. 编码约定

- `gofmt` 必须干净；提交前跑 `go vet ./...`
- 错误用 `fmt.Errorf("...: %w", err)` 包装；对外只暴露统一的错误 envelope
- 每个函数第一参数是 `context.Context`（有 IO 或可能阻塞时）
- `main` 只做一件事：调用 `run() error`，失败时打日志并 `os.Exit(1)`
- 不用 `init()`，不用包级可变状态
- 注释用中文，解释「为什么」，不复述代码；导出标识符写 doc comment
- 标识符、包名、文件名用英文；包名短小，禁止 `util`、`common`、`helpers`
- JSON 编码关闭 HTML 转义（`enc.SetEscapeHTML(false)`），否则中文音色 ID 会变成 `\uXXXX`
- 测试表驱动，用例名用中文描述场景；测试不得访问真实网络

## 6. 命令

```bash
go build ./...
go vet ./...
go test ./...
gofmt -l ./cmd ./internal

docker build -t mimivoice2api:dev .
docker run --rm -p 8080:8080 --env-file .env mimivoice2api:dev

curl -sS localhost:8080/healthz
curl -sS localhost:8080/v1/models -H "Authorization: Bearer $AUTH_TOKEN"
curl -sS localhost:8080/metrics -H "Authorization: Bearer $AUTH_TOKEN"

git tag -a v1.0.0 -m "v1.0.0" && git push origin v1.0.0   # 触发发布
```

`-race` 需要 CGO 与 gcc；本机没有编译器时不要把它当成本地门槛，交给 CI（`.github/workflows/ci.yml`）。

验收以本地 `go build` 产物 + curl 冒烟为准；Docker 实装测试不是门槛（用户约定 2026-10-28）。

冒烟测试用 `testdata/audio/` 下的合成音频，不要提交真实人声。

## 7. 契约同步

- [docs/api-spec.md](docs/api-spec.md) 的映射表是**唯一事实来源**
- 改行为先改表，再改代码；代码与表不一致视为 bug
- 上游字段变化时，同步更新「上游契约快照」小节与核对日期
- 新增或删除对外参数，必须同步 README 的能力矩阵

## 8. 完成定义（DoD）

一个改动只有同时满足以下条件才算完成：

1. `go build ./...`、`go vet ./...`、`go test ./...` 全绿（CI 另跑 `-race`）
2. 新增行为有对应测试；映射逻辑有表驱动用例
3. 日志中没有音频、base64、转写文本、密钥
4. 相关文档已更新，链接可达
5. 没有引入未在文档中声明的新依赖或新配置项
6. `git status` 干净，没有误提交的 `.env`、构建产物、临时文件

Docker 构建与真实上游 key 联调属于可选项，不作为 DoD 门槛。

## 9. 提交规范

- Conventional Commits，描述用中文：`docs: 补齐 TTS 参数映射与错误码约定`
- 一个 commit 只做一件事；文档与实现同属一件事时放同一个 commit
- 默认直接在 `main` 上开发（单人项目）；需要评审时开 `feat/<短名>`
- 已获用户授权（2026-10-18）：commit、push、开分支、触发 CI 无需逐次确认；远程配置变更仍需说明理由
- 版本标签从 `v0.1.0` 起：M1 → `v0.1.0`，M2 → `v0.2.0`，M3 → `v0.3.0`，M4 → `v1.0.0`

## 10. 安全与隐私

- `.env` 永不入库；`.env.example` 只放占位值
- `AUTH_TOKEN` 为空时启动打 WARN，提示仅限本机调试
- 鉴权比较用 `crypto/subtle.ConstantTimeCompare`
- 上游密钥只出现在发往 MiMo 的请求头里；错误信息、日志、响应体都不得回显
- 容器以非 root 运行，根文件系统只读，`tmpfs` 挂 `/tmp`，`cap_drop: ALL`
- 不实现 CORS（服务端到服务端调用），需要时由前置反代处理

## 11. 上游变更时的复核清单

MiMo 文档更新后，按顺序核对并回填 [docs/api-spec.md](docs/api-spec.md)：

1. 端点路径与鉴权头是否仍是 `POST /v1/chat/completions` + `Authorization: Bearer`
2. ASR 的 `input_audio` 是否仍只接受 `mp3` / `wav`；是否新增 `format` 约束
3. TTS 的 `audio.format` 枚举、`stream=true` 时的默认格式是否仍是 `pcm`
4. `choices[0].message.audio.data` / `delta.audio.data` 是否仍是 base64
5. `usage.seconds` 是否仍存在（`verbose_json` 的 `duration` 依赖它）
6. 内置音色列表是否有增删

## 12. 文档写作约定

- 中文为主，术语保留英文原文（token、base64、SSE、multipart）
- 结论先行，表格优先；不确定的地方标注「待实测」，不要编造
- 每个「不支持」都要写清替代方案或错误表现
- 不写营销腔，不写无法验证的性能数字
