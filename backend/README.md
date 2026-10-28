# 后端目录与开发

后端使用模块化单体组织代码，API 与 worker 独立启动、共享业务模型与应用协作逻辑。

```text
backend/
├── cmd/                         # API、worker、迁移、备份等进程入口
└── internal/
    ├── bootstrap/               # API 与 worker 共享的依赖装配
    ├── modules/                 # 可独立测试的业务模型与规则
    │   ├── identity/            # 身份、组织、角色、凭据摘要
    │   ├── projects/            # 简报、剧本、分镜、深拷贝与校验
    │   ├── generation/          # 图像参数、默认值、生成结果与素材
    │   ├── media/               # 时间轴、字幕、关键帧、海报及校验
    │   ├── publishing/          # 发布意图、平台、账号和交付记录
    │   └── governance/          # 模型配置、执行契约、报价摘要、币种规则
    ├── application/             # HTTP、任务编排及当前持久化协作核心
    ├── platform/                # 可复用的基础设施
    │   ├── jobqueue/            # PostgreSQL 队列、租约和 fenced commit
    │   ├── tracing/             # 链路上下文、采样和导出
    │   └── securebackup/        # 加密备份流
    └── architecture/            # 包依赖约束测试
```

## 依赖方向

`cmd → bootstrap / application → modules / platform`。

`modules` 只能依赖标准库和显式批准的业务值类型：`projects → governance` 用于持久化的模型选择；`media → publishing` 用于兼容现有媒体快照中的发布状态。业务模块不依赖 HTTP、SQL、供应商 SDK、文件存储、`application` 或 `bootstrap`。`platform` 不反向依赖业务模块或启动装配。这些规则由 `internal/architecture` 中的测试检查。

## 修改代码时的归属

- 输入校验、字幕时间、时间轴、金额精度等纯规则放入对应业务模块。
- 接单、组织授权、预算预留、入队、结果确认等协作流程目前在 `application` 中维护。
- API 与 worker 的模型默认配置统一经过 `bootstrap.ConfigureProviders`，避免两份装配代码发生漂移。
- 新增与业务无关的基础设施放入 `platform`，不要让业务模块直接依赖具体数据库或供应商客户端。

`application/*_contracts.go` 使用类型别名和普通转发函数承接原有调用方，JSON 字段、错误身份、自定义 JSON 解码与持久化格式保持兼容。新业务规则应直接写入对应模块，并使用外部测试包验证公开边界。

## 构建与检查

使用 `go.mod` 声明的 Go 1.25.3。当前文件锁、路径保护与媒体处理面向 Linux；本机 Go 版本过新时应选择相同工具链。

```sh
gofmt -w cmd internal
go vet ./...
go test -race ./...
go build ./cmd/...
```

媒体回归需要 FFmpeg / FFprobe 和中文字体。真实 PostgreSQL、S3 等集成测试需显式提供专用测试环境，未配置时会跳过。仅验证纯业务包和目录边界可运行：

```sh
go test ./internal/modules/... ./internal/architecture
```

## 当前迁移边界

本轮完成目录分层、业务模型与纯规则提取、共享装配及依赖检查。`application` 内仍保留原有供应商适配、HTTP 和持久化实现，`FileStore` 仍是现有事务协作的入口；这些实现没有通过目录迁移自动解耦。

PostgreSQL 的 scope 快照读取方式、`stateMutex` 事务语义、费用预留与任务入队的原子性均沿用原实现。后续应按业务用例逐个引入显式事务接口和查询仓库，再迁移具体适配器；不能为了目录拆分而破坏当前跨模块的一致性。
