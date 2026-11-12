# 安全的后端错误诊断

- 日期：2026-11-12；状态：Implementing（本地完整与云端检查尚在进行）。
- 代码基线：`f03094b413628acb83f174cd14ad6a4ca16f5f41`；其父远端基线为 `0e49adf5c329b9918c86ff87461acd56559b71e1`。
- 用户授权：完善后端日志和接口错误；通过检查后推送并跟踪 CI。禁止生产部署、真实付费调用、生产模型配置修改。
- 本轮复用现有 audit/trace、ModelCall 和持久任务，不新增追踪平台、费用或配额门槛。

## 用户故事与需求

US-D1：创作者生成失败时可提供安全的关联编号，管理员能够找到对应请求和异步执行，而不要求用户复制密钥或完整请求。

US-D2：管理员可区分配置、输入、权限、存储、上游拒绝及执行中断；当供应商没有提供具体信息时，系统明确标注原因未知。

| 需求 | 实现边界 | 验收 |
| --- | --- | --- |
| FR-D1 服务生成请求 ID 与调用链关联 | `diagnostics_http.go`、现有 tracing 和 Operations；忽略客户端伪造的 X-Request-ID | AC-D1 |
| FR-D2 兼容 JSON 错误并提供建议 | 既有 code/message/field 不删除；OpenAPI 新增可选字段 | AC-D2 |
| FR-D3 上游安全元数据 | 共用请求观察器；状态/允许的代码/编号，未知内容指纹化 | AC-D3 |
| FR-D4 同步与异步失败可定位 | 任务持久 trace、ModelCall、领域完成日志、file/PG worker | AC-D4 |
| FR-D5 panic 不泄密且保持恢复语义 | HTTP、Eino 前置 provider guard、ASR、本地队列、PG worker、启动边界 | AC-D5 |
| NFR-D1 不泄露敏感内容 | 不记录 raw error/body/URL/headers/prompt/个人身份；保留原私有审计合同 | AC-D3/5 |
| NFR-D2 不增加供应商调用或破坏幂等 | 只观察已读字节；未知结果不自动重发 | AC-D3/5 |

## 设计与数据兼容

实际代码路径：

- `backend/internal/platform/diagnostics/{diagnostics,codes}.go`：受控事件、分类、指纹、JSON 日志、panic 安全位置与启动阶段。
- `backend/internal/application/diagnostics_{http,upstream}.go`：请求边界、路由模板和有限元数据观察。`http.go`、`auth_http.go`、`operations.go` 保持认证、CORS 及审计行为。
- `public_execution.go`：只在 API 副本上附加诊断，不改变领域对象持久化格式。
- `gateway_{transport,runtime}.go`、`model_usage.go`：模型调用保留关联、上游码、最后响应状态和安全编号；旧 JSONB/文件可缺省新增字段。
- `pipeline.go`、`project_pipeline.go`、`storyboard_pipeline.go`、`alignment_provider.go`：在库格式化 panic 前捕获，不输出异常值。
- `internal/platform/jobqueue/worker.go`：释放执行槽、取消续租、保留派发围栏；不把异常执行当成已成功。
- `cmd/{server,worker}/main.go`：控制台 JSON 与安全的启动/服务阶段；net/http 的原始错误输出替换为受控事件。
- `api/openapi.json`：ErrorDiagnostic、UpstreamDiagnostic、任务和调用记录的可选字段。使用及故障排查合同见 [专题说明](../../backend-error-diagnostics.md)。

无 SQL DDL、强制数据回填或旧文件改写。回退旧二进制会忽略新增 JSON 字段，原 trace 与任务/调用状态保留。旧日志不自动清除。未修改暂停短剧工作区、前端样式、模型能力勾选、权限、审核策略和网关目标安全策略。

## 可测试验收标准

- AC-D1：Given 客户端发送伪造编号、敏感 URL/query/cookie；When 两次失败请求；Then 返回不同的服务编号、可关联 trace，控制台不包含这些输入。
- AC-D2：Given 现有版本冲突；When JSON 错误；Then 原 code/message 保留，附加分类、建议及编号，OpenAPI 验证和既有 API 合同通过。
- AC-D3：Given 本地上游 400 带公开错误码、UUID 与恶意 message；When 请求失败；Then 安全状态/代码/编号可用，message 不进入响应或日志。未识别字段指纹化；观察器 Close 不额外读取、不重试。
- AC-D4：Given 持久任务及 ModelCall；When 执行失败或查询；Then 可通过持久 trace 关联，权限仍由原租户作用域控制，旧记录不伪造编号。
- AC-D5：Given HTTP/Eino/provider/worker panic；When 捕获；Then 无原始异常泄露，HTTP 未提交返回安全 500、已提交中断；审计结束记录正确，PG 已派发任务保留 fence，执行槽释放且不重发。

## 任务与验证记录

- [x] T-D1 关联与 JSON 响应、上游有限观察、模型调用元数据（FR-D1–3）。
- [x] T-D2 同步/异步失败与 panic 边界、trace 恢复（FR-D4–5）。
- [x] T-D3 OpenAPI、运维文档、确定性泄密与围栏测试（AC-D1–5）。
- [ ] T-D4 完整文件/PG race、静态、HTTP/媒体/浏览器合同，记录准确通过与未测项。
- [ ] T-D5 最终审查、远端安全核对、提交推送及完整 CI 到终态；禁止部署。

已执行（最终提交前记录，云端待补）：

- 定向 `-race`：diagnostics、application、jobqueue 通过；**真实临时 PostgreSQL** 上实际执行新增 worker panic 派发围栏专项（未跳过），application 14.529 秒，jobqueue 1.246 秒。
- 文件模式完整补集：a-m 通过（application 230.735 秒），other 通过（145.053 秒）；native 与完整 PG 补集仍运行中。
- Go vet、全部命令构建、OpenAPI 校验、78 项前端单测、lint、typecheck 和构建通过。
- 24 项桌面/手机认证浏览器验收通过（1.8 分钟），覆盖角色、撤销、配置、CORS/CSRF 和空间隔离；网关创作浏览器回归仍运行中。
- 安全接口合同 238 个响应通过；通用、故事、迭代、媒体、字幕对齐合同及崩溃恢复 smoke 通过。Python 17 项与本地模型 runner 12 项测试、live 调用授权防护及离线恢复检查通过，真实供应商调用数为零。
- API/worker 实际二进制在包含合成敏感信息的不可用数据库连接下均退出 1，输出 JSON `startup_error/component=storage`，没有回显连接串或敏感值。
- 回归曾发现通用提示覆盖已有“预演不能发布”指引：改为领域内类型化验证提示，保留已有文案并剔除任意包装错误文本；安全合同重跑通过，新增防泄漏定向测试通过。
- 开发中出现过短暂结构字段编译不同步，已修复；以最终提交及云端检查为准。

## 风险及未验证边界

未知上游内部原因、请求未返回元数据、未消费/非 JSON/SSE/超过观察缓冲正文时，只能定位本地阶段和已有状态，不保证取得真实供应商原因。指纹化的非标准 ID 无法直接查询供应商。连续性检查详情暂沿用原领域形状，通过执行日志和 ModelCall 关联。非 provider 的文件回调异常可能依赖重启恢复。真实供应商、生产日志、许可和账号配置未验证；此前生产诊断端点被环境代理拒绝（403），没有更改生产状态。
