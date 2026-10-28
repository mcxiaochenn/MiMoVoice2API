# 目录与工程约定

## 1. 目标态目录树

标注为该目录出现的里程碑；M0–M4 已全部落地，下面的树就是当前实际结构（唯一例外是测试文件未列出）。

```text
MiMoVoice2API/
├── AGENTS.md                     M0   AI 代理与贡献者约束
├── README.md                     M0   项目说明与使用入口
├── LICENSE                       —    MIT 许可证（SPDX: MIT）
├── .env.example                  M0   配置样例（只有占位值）
├── .gitignore                    M0
├── .gitattributes                M1   统一 LF，标记二进制样例
├── .github/workflows/
│   ├── ci.yml                    M4   gofmt / vet / test -race / 双架构构建
│   └── release.yml               M4   tag 触发 GHCR 推送、release 与二进制附件
├── docs/
│   ├── architecture.md           M0   架构、流水线、内存与部署
│   ├── api-spec.md               M0   下游契约与上游映射（唯一事实来源）
│   ├── conventions.md            M0   本文件
│   └── roadmap.md                M0   里程碑与验收
├── go.mod                        M1   module github.com/mcxiaochenn/MiMoVoice2API（零依赖，无 go.sum）
├── Dockerfile                    M1
├── .dockerignore                 M1
├── docker-compose.yml            M1
├── cmd/
│   └── mimivoice2api/
│       └── main.go               M1   只做装配：config → router → server → 优雅退出
├── internal/
│   ├── config/
│   │   └── config.go             M1   env → Config，启动即校验
│   ├── httpapi/
│   │   ├── router.go             M1   路由表与中间件串联
│   │   ├── middleware.go         M1   request id、recover、鉴权、body limit、访问日志、指标采集
│   │   ├── transcriptions.go     M1/M3 非流式 + SSE 增量转写
│   │   ├── speech.go             M2/M3 非流式 + 二进制/SSE 流式合成
│   │   ├── sse.go                M3   下游 SSE 写出与流内错误
│   │   ├── metrics.go            M4   /metrics handler 与指标记录
│   │   ├── models.go             M1
│   │   ├── health.go             M1
│   │   ├── dto.go                M1/M3 下游请求/响应/事件结构
│   │   └── errors.go             M1   OpenAI 错误 envelope
│   ├── adapter/
│   │   ├── asr.go                M1   纯映射
│   │   ├── tts.go                M2
│   │   ├── model.go              M1   模型别名
│   │   ├── voice.go              M2   音色别名与克隆样本判定
│   │   └── errors.go             M1   字段级错误类型
│   ├── mimo/
│   │   ├── client.go             M1   客户端、状态码分类、流式调用、429 重试
│   │   ├── types.go              M1   上游请求/响应结构
│   │   └── sse.go                M3   上游 SSE 解析、截断判定
│   ├── metrics/
│   │   └── metrics.go            M4   零依赖 Prometheus 文本格式实现
│   └── audio/
│       └── sniff.go              M1   魔数嗅探、格式 ↔ MIME
├── testdata/
│   └── audio/
│       ├── README.md             M1   样本来源与生成命令
│       └── sample-1s.wav         M1   合成正弦波，不含人声
```

### 1.1 不新增的目录

| 不建 | 替代 |
| --- | --- |
| `pkg/` | 没有对外复用需求，全部放 `internal/` |
| `api/` | 契约写在 `docs/api-spec.md`，不维护 OpenAPI 文件直到有代码生成需求 |
| `migrations/`、`db/` | 无数据库 |
| `scripts/` | 命令写进文档；真的需要时用 `Makefile`，不堆散装脚本 |
| `internal/util`、`common`、`helpers` | 按职责命名；找不到名字说明职责没想清 |

## 2. 分层与依赖

```text
cmd/mimivoice2api  →  internal/httpapi  →  internal/adapter  →  internal/mimo
                          │                     │
                          └──→ internal/audio ←─┘
```

| 包 | 可以 import | 禁止 |
| --- | --- | --- |
| `cmd/*` | 所有内部包 | 业务逻辑 |
| `httpapi` | `adapter`、`mimo`、`audio`、`config` | 直接拼上游 JSON |
| `adapter` | `mimo`（仅类型）、`audio`（仅枚举） | `net/http`、`os.Getenv`、写日志 |
| `mimo` | 标准库 | 其它内部包 |
| `audio` | 标准库 | 其它内部包 |
| `config` | 标准库 | 其它内部包 |

`adapter` 的输入是自定义结构体，不是 `*http.Request`；这样映射可以单测，也能在未来复用到别的传输层。

## 3. 命名

| 对象 | 约定 | 示例 |
| --- | --- | --- |
| module | `github.com/mcxiaochenn/MiMoVoice2API` | 仓库归属变化时全局替换 |
| 二进制 | `mimivoice2api` | 与目录名一致 |
| 包名 | 小写单词，无下划线 | `httpapi`、`adapter` |
| 文件 | 小写，按职责或路由命名 | `transcriptions.go`、`sniff.go` |
| 接口 | 单方法用 `-er` | `Doer`、`Decoder` |
| 测试 | 与被测文件同名 + `_test.go` | `asr_test.go` |
| 环境变量 | 大写下划线，上游相关加 `MIMO_` | `MIMO_API_KEY`、`MAX_TTS_CHARS` |
| 镜像 | `ghcr.io/<owner>/mimivoice2api` | tag：`latest`、`v0.2.0`、`sha-1a2b3c4` |
| 里程碑 | `M0` ~ `M4` | 与 roadmap 一致 |

## 4. 代码风格

- Go 1.26，`gofmt` 必须干净，导入分组交给 `gofmt`，不引第三方 linter 作为硬门槛
- 只用标准库。引入依赖需要同时满足：标准库确实做不到、可静态编译、维护活跃；并在 commit 说明里写体积代价
- `CGO_ENABLED=0`
- 错误包装用 `%w`，错误字符串小写开头、不带句号
- `main` 只调用 `run() error`；`run` 里完成装配并返回错误
- 不用 `init()`、不用包级可变状态、不用 `panic` 做控制流（recover 中间件除外）
- 有 IO 或可能阻塞的函数，第一参数是 `context.Context`
- 导出符号写 doc comment；注释解释「为什么」，不复述代码
- JSON：`enc.SetEscapeHTML(false)`；解码未知字段时保持宽松（不启用 `DisallowUnknownFields`）
- 时间、随机数、HTTP 客户端可注入，便于测试；不在函数内部直接取 `time.Now()` 参与断言

## 5. 配置

- 唯一入口 `internal/config`，启动时解析一次，之后按字段注入
- 校验失败立刻退出，错误信息指出变量名与期望格式，不回显值
- 默认值写在 `config.go`，`.env.example` 与 README 表格必须与代码一致
- 不实现热更新；改配置 = 重启容器
- 新增配置项需要同时改：`config.go`、`.env.example`、README 配置表、api-spec（若影响契约）

## 6. 日志

固定字段见 [architecture.md](architecture.md#10-日志与可观测性)。补充约定：

- 一条请求一条访问日志，包含 `request_id`、`route`、`status`、`dur_ms`
- 错误日志带上 `err`、`upstream_status`、`err_code`，不再重复打印整个请求
- `debug` 级别才记录字段级细节（如 language 回落原因）
- 禁止：音频字节、base64、转写/合成文本、API Key、完整请求头
- 文本类字段只记长度：`text_len`、`instructions_len`

## 7. 测试

| 层级 | 位置 | 要求 |
| --- | --- | --- |
| 单元 | `internal/*/*_test.go` | 表驱动；覆盖别名解析、格式映射、错误分类、魔数嗅探、指标渲染 |
| 契约 | `internal/httpapi/*_test.go` | `httptest.NewServer` 假上游，断言发出去的 JSON 与返回的下游响应 |
| 冒烟 | 手动 + 真实 key | 每次发版前跑一次，结果记录在 PR/commit 里 |
| CI | `.github/workflows/ci.yml` | `gofmt` / `vet` / `test -race` / 双架构 docker build，全部离线 |

规则：

- 测试不访问真实网络；需要上游时用 `httptest` + 固定 JSON
- 测试数据放 `testdata/`，音频只用合成音（正弦波/静音），不提交真实人声
- 断言错误时同时断言 HTTP 状态码与 `code`
- 不追求覆盖率数字；映射表的每个分支至少一条用例

## 8. Git

- 主分支 `main`，单人开发直接提交；需要评审时开 `feat/<短名>`
- Conventional Commits，描述中文：`feat: 实现 ASR 非流式转写`
- 一个 commit 一件事；格式化、重命名单独提交
- 不提交 `.env`、音频、二进制、镜像产物
- 标签：M1 `v0.1.0`，M2 `v0.2.0`，M3 `v0.3.0`，M4 `v1.0.0`；推送 tag 会触发发布工作流
- 已获用户授权（2026-10-18）：commit、push、开分支、触发 CI 无需逐次确认；发版 tag 的版本号需与路线图一致

## 9. 文档

- `api-spec.md` 是契约源：改行为先改表
- `architecture.md` 记录结构性决策与坑位；新增坑位补进第 12 节表格
- 里程碑状态只在 `roadmap.md` 更新，README 只写当前阶段一句话
- 不新建 ADR 目录；决策写进对应文档的「取舍」小节，保持单一入口
- 文档内链接使用相对路径，保证 GitHub 与本地都可点

## 10. 依赖引入门槛

按顺序自问：

1. 标准库能否在 50 行内解决？（`net/http`、`encoding/json`、`mime/multipart`、`log/slog`、`encoding/base64` 基本覆盖本项目）
2. 引入后是否破坏 `CGO_ENABLED=0` 静态编译？
3. 镜像体积增加多少？是否值得？
4. 维护者是否活跃、许可证是否兼容？

预期结论：M1–M4 零第三方依赖；指标选择手写 `/metrics` 文本格式（`internal/metrics`），而不是拉 Prometheus 客户端，实现约 300 行。
