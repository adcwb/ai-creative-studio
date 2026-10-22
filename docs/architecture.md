# 第一阶段架构与边界

## 当前可运行流程

```mermaid
flowchart LR
  UI[React 中文工作室] --> API[Go HTTP API]
  API --> Store[单写者 JSON 文件仓库]
  Worker[进程内 worker] --> Store
  Worker --> Eino[Eino Chain]
  Eino --> Normalize[输入校验和规范化]
  Normalize --> Mock[Mock SVG Provider]
  Mock --> Store
  Store --> Assets[素材元数据与 SVG]
```

Eino `compose.NewChain[Input,string]` 实际编译和调用两个 Lambda 节点：Normalize 去除首尾空白并验证 Unicode 字符长度/类型；ImageProvider.Generate 生成确定性、转义安全、带模拟标识的 SVG。测试覆盖编排成功、输入失败、provider 失败和取消。代码没有真实模型调用、API key 或网络生成依赖。设计参考 [Eino 官方组合编排文档](https://github.com/cloudwego/eino#composition)。

API 输入也先验证，避免将无效任务放入持久队列；流程内部再次校验，使未来独立 worker 仍能验证自己的输入。进度只表示阶段：queued 0、running 35、终态 100，不代表真实模型百分比。mock 默认延迟 900ms 以便观察状态。

## 持久化与执行语义

`Repository` 是创建/查找/列举/领取/完成/保存素材的边界，当前仅 FileStore 实现。`ImageProvider` 是模型边界，当前仅 MockProvider。任务 ID 使用随机 128 位标识；每个任务和素材有 `space_id`。幂等键按空间范围持久化，同键同内容返回相同任务，同键不同内容返回 409。素材 ID 等于任务 ID，因此重复保存无副作用。

单 mutex 保护进程内一致性；文件锁防止多进程同时打开；写入先构造新快照，写临时文件并 fsync，再 rename，成功后才替换内存快照。进程崩溃最多留下一个不可见临时文件。未完成任务会在下一次启动恢复为 queued，重新执行。取消服务时保留 running 状态以供恢复。执行器持久化失败会使服务退出，避免继续接受任务但不再执行的假健康状态。

这是 **至少一次** 执行，不是 exactly-once。mock 确定性且无收费副作用；将来真实 provider 必须支持外部幂等键、回执查询及重试成本治理。当前单 worker 顺序执行，最长输入 1000 Unicode 字符，请求体最多 16KiB。文件仓库存储全部历史，列表没有分页；只适合小规模演示。目录 rename 后未额外 fsync 目录，整机断电可能丢失最近变更，不宣称数据库级持久性。需要停服备份；文件损坏时拒绝启动，由操作者从备份恢复。

## 个人与团队数据归属

当前内置 `personal-demo`（owner: demo-user）与 `team-demo`（owner: demo-team）。所有任务查询、结果读取和素材保存均按空间过滤，跨空间 ID 返回 404；接口测试覆盖此行为。浏览器切换空间会重新挂载页面，清空旧状态和取消旧轮询。

**演示空间完全公开，无认证、无成员关系检查。** 客户端可自由选择任一演示空间。CORS 只约束浏览器来源，不是授权。生产设计需 `users`、`organizations`、`spaces`、`memberships`，个人空间绑定用户、团队空间绑定组织，服务从经过验证的身份解析主体并检查空间角色，存储查询携带服务端授权的 space_id。对象存储路径应形如 `spaces/{space_id}/assets/{asset_id}`，并通过授权后的短期签名 URL 访问，不能依赖前端过滤。尚未实现 SSO、RBAC、租户级数据库策略、审计与企业隔离。

## 后续适配路径（均未接入）

1. PostgreSQL：替换 Repository；表包括 spaces、memberships、image_tasks、assets、outbox，约束 `(space_id,idempotency_key)` 与 `(space_id,task_id)` 唯一。事务提交任务与 outbox，避免数据库成功而消息丢失。
2. Redis / Asynq：将 outbox 事件投递至独立 worker。引入租约、退避重试、失败队列、超时、取消和可观测性；不能复用 FileStore 做多机队列。
3. 对象存储：将 SVG/真实媒体移出 JSON，保存对象引用、校验和和 provider 元信息；考虑对象提交和任务完成之间的孤立对象回收。当前没有空壳对象存储适配器，避免误导为已实现。
4. 短剧 / 漫剧：项目 → 创意分析 → 剧本 → 分镜 → 角色一致性资产 → 音视频生成 → 合成。可用 Eino Graph 编排长流程，但当前未提供这些 API 或生成入口。
5. 发布前：真实鉴权、用量限制、内容审核、隐私授权、图片上传校验、密钥托管、账单、故障恢复、依赖安全审查及负载测试。仓库公开不等于产品获准公开部署。

采用模块化单仓库而非多仓库，是为了让一个 PR 同步修改 UI、Go 实现和契约；仍然独立依赖、独立启动、独立构建。没有引入现阶段不需要的基础设施依赖或空服务。
