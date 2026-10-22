# 第四轮架构：短剧项目与模拟剧本

本轮从 `7dab4ea` 开始，只实现路线中的“项目 → 创意需求 → 结构化剧本 → 编辑保存”。单项目只有一份当前稿，不增加分镜、视频、角色资产库、多版本回滚、数据库、认证或付费文本模型。[OpenAPI 0.4.0](../api/openapi.json) 是字段与响应的权威合同；产品验收见 [第四轮产品](iteration-4-product.md)。本轮仅本地开发和验证，不推送或部署。

业务 API、执行排队、Eino 编排、校验和文件持久化均真实运行；`mock-script` 的剧本文本是确定性程序产物，不是模型创作。该流程独立于图像 `capabilities`、图像 provider 和 `OPENAI_API_KEY`。即使图像配置不可用，短剧仍可正常操作；本轮开发与测试不读取真实凭据或发送付费调用。

## 领域与数据

不把短剧塞进现有 Image Task。新增 Project、Script、ScriptGeneration 和项目操作回执，仍由原 FileStore 单写者锁和单 mutex 保护。

| 对象 | 公开字段与含义 |
| --- | --- |
| Project | `id,space_id,title,brief,version,created_at,updated_at`；可选 `script,script_brief,latest_generation` |
| Script | `title,logline,characters,scenes`；恰 2 个角色、恰 3 场 |
| Character | `name,description`，规范化后名称互异 |
| Scene | `heading,action,dialogue`，每场 1–6 句对白 |
| Dialogue | `character,text`，角色名必须引用当前 Script 中的一个角色 |
| ScriptGeneration | `id,space_id,project_id,source_title,source_brief,base_version,provider,status,progress,created_at,updated_at`；失败时 `error,error_code` |

Project 无稿时省略 `script/script_brief`，没有生成尝试时省略 `latest_generation`，不用 null。`latest_generation` 是完整尝试投影，前端只轮询项目详情即可。尝试历史记录输入来源和执行结果状态，不保存可回滚的历史剧本版本。

`script_brief` 是当前稿生成时的需求快照。更改项目 title/brief 或手工编辑剧本都保留它；下一次生成成功才替换。当前 brief 与它不同时，界面说明当前稿来自旧创意。生成失败也不清空已有稿件或这个来源。不能仅凭最新尝试成功/失败判断项目是否还有可编辑旧稿。

所有新 ID 由服务端随机生成 128 位小写十六进制字符串。JSON 回执、内部键和存储路径不公开。返回或复制含切片的 Script 必须深拷贝，防止调用方修改、未成功的快照写入或并发编辑污染已提交内存状态。

## 请求与校验

所有文本 trim 后按 Unicode 字符数校验。字段必须是规定类型且非 null，禁止未知字段；不接受用空对象冒充合法剧本。

| 文本 | 最小–最大字符数 |
| --- | --- |
| 项目 title / Script title | 1–80 |
| 项目 brief | 3–2000 |
| logline | 3–300 |
| character name | 1–40 |
| character description | 1–200 |
| scene heading | 1–100 |
| scene action | 1–1000 |
| dialogue text | 1–300 |

项目写请求只接受单个 `application/json` 对象，最多 **64 KiB**。图像 API 原有 **16 KiB** 限制不变。`expected_version` 是必填正整数，缺失、null、零和类型错误均拒绝。客户端不能提交 script provider、任务状态、来源快照或服务端时间。

前端编辑器只提供 Script title/logline、场景 heading/action 和对白 text；角色信息、对白归属及行结构只展示，不提供增删按钮。这是本轮界面范围，**并非 API 的字段权限声明**：PUT/script 接收完整结构，允许结构约束内的合法角色或对白修改，并完整校验角色唯一性、引用及数量。`script_brief` 属于 Project 服务端元数据，既不在 Script 内，也不在保存请求中，始终不能由客户端覆盖。

## HTTP 合同

所有路由在 `/api/v1` 下，项目操作均要求有效 `space_id`。未知项目和跨空间 ID 一律 404。空间是公开演示数据归属选择，仍没有身份验证或企业隔离。

| 方法与路径 | 请求 | 成功响应 |
| --- | --- | --- |
| GET `/projects` | `space_id` | 200 `Project[]`，按 updated_at 倒序，ID 稳定排序 |
| POST `/projects` | `{title,brief}` + 幂等键 | 新建 201 Project；重放 200 当前 Project |
| GET `/projects/{id}` | `space_id` | 200 当前 Project，包含可选 latest_generation |
| PUT `/projects/{id}` | `{title,brief,expected_version}` + 幂等键 | 200 当前 Project |
| GET `/projects/{id}/generations` | `space_id` | 200 `ScriptGeneration[]`，按 created_at 倒序，ID 稳定排序 |
| POST `/projects/{id}/generations` | `{expected_version}` + 幂等键 | 新建 202 queued 尝试；重放 200 原尝试当前状态 |
| PUT `/projects/{id}/script` | `{script,expected_version}` + 幂等键 | 200 当前 Project |

列表直接返回数组，空列表为 `[]`，无分页。没有删除、取消执行、批量操作或单独的 retry endpoint。失败后再次生成使用新幂等键创建新的真实执行尝试；原记录保留。生成使用已经保存的 title/brief，不能把未保存浏览器内容伪称为已生成来源。

错误沿用 `{error:{code,message}}`。参数、键、空间和 JSON 错误为 400；JSON 语法、无法解码的类型、未知字段和显式 null 为 `invalid_json`，规范化后的缺失必需值、长度或结构语义为 `invalid_input`。项目请求逐 token 拒绝 null，避免重复字段之后的 null 被 Go 解码器忽略而保留旧值。来源拒绝 403；对象不存在/跨空间 404；内容类型错误 415；存储错误 500。项目 409 分类如下：

| code | 客户端行为 |
| --- | --- |
| `idempotency_conflict` | 同键已用于不同操作、项目或内容；不能把修改后的内容当作原提交重发 |
| `version_conflict` | 后端内容已更新；保留本地草稿，明确重载/放弃后再编辑，不自动覆盖 |
| `generation_active` | 当前项目已有 queued/running 尝试；等待终态，不创建第二个尝试 |
| `script_missing` | 尚无生成成功的剧本；先生成再编辑保存 |

## 版本与幂等

Project 初始 `version=1`。每次新的项目内容保存或剧本保存，即使规范化内容未改变，也递增 1；生成成功替换当前稿时递增 1。生成接受、领取和失败只更新相关尝试/时间，内容版本不增加。只有当前项目被活动生成锁定，其他项目仍可操作。

所有项目写入都要求 `Idempotency-Key`，格式沿用 8–128 位字母、数字、下划线或连字符。项目域在同一空间内共享键命名空间：创建、更新、生成和保存之间混用键会冲突；它与原图像任务键命名空间独立，不改变旧键语义。

回执记录操作类型、目标项目、规范化完整请求（包含 expected_version）及成功关联。必须和业务变更在同一快照内提交。请求边界先校验输入与空间/对象归属；仓库锁内流程为：

1. 匹配该空间项目域的键；身份不一致为 idempotency_conflict，精确匹配立即重放。
2. 新请求检查 expected_version；不一致为 version_conflict。
3. 检查当前项目是否已有活动生成；有则 generation_active。手工保存还要求已有稿件。
4. 构造独立新快照，一起提交业务变更与回执，持久化成功后才对外确认。

**回执证明操作已提交，不是原响应的历史副本。** 创建/更新/保存重放返回项目当前投影，可能含后续更新，但不重新应用原写入、回滚旧内容或再次增加版本。生成重放返回原 attempt 的当前状态，不返回另一个最新尝试。这个选择避免把已过期的响应快照当成当前内容。

网络错误、响应丢失或 500 时，前端保留原键、原 body 与未保存草稿；只重发同一次请求。不能刷新 expected_version 后沿用原键，也不能为了消除冲突自动换键重写。已接受请求即使项目版本已变化、正在下一次生成，仍可精确重放。

## Eino 执行与并发

新增独立 ScriptPipeline，真实编译并调用三个节点：Normalize 规范化持久化输入快照 → MockGenerate 生成确定性 Script → Validate 复核完整输出。API 入队前校验与 Eino 内复核均保留。不得用 HTTP handler 内固定成功 JSON 或浏览器拼接文本绕过执行器。

生成 POST 原子写入 queued 尝试、项目最新尝试关联和请求回执，保存 `source_title/source_brief/base_version`。独立脚本 worker 先持久化 running，再调用 Eino；成功时在锁内复核项目和尝试关联，将已校验的 Script、script_brief、内容版本和 succeeded 状态一次提交。不得从执行完成时的可变项目字段重新猜测生成输入。

queued/running 期间，项目内容更新、手工保存和第二次新生成均拒绝。这使生成结果不能覆盖执行期间的新人工编辑。领取和完成仍须在同一仓库 mutex 下读写；不能仅靠前端禁用按钮保证一致性。

| 尝试状态 | progress | 语义 |
| --- | --- | --- |
| queued | 0 | 请求已持久化，等待真实 worker |
| running | 35 | 执行阶段已持久化，正在调用 Eino |
| succeeded | 100 | 合法稿件与状态已原子保存 |
| failed | 100 | 安全错误已保存，旧稿及内容版本保留 |

brief 中显式 `[mock-fail]` 触发 `mock_script_failed`，便于真实失败路径验收；界面不自动删标记。输出结构校验失败为 `invalid_script_output`。错误文案为静态安全说明，不透传内部异常、文件路径或调用栈。

## 快照与恢复

沿用原 DATA_DIR、排他文件锁、临时文件/fsync/rename 与单 mutex，**不另开一个彼此不一致的项目仓库**。快照升级为 version 2，保留 tasks/assets，增加必需映射 `projects/script_generations/project_receipts`。这只是本地 JSON 格式的受控升级，不引入数据库或迁移框架。

- 不存在 state.json：初始化完整 version 2 空库。
- 完整 version 1：先通过原 version/tasks/assets 校验，再补空项目域并原子提交 version 2。旧图像 ID、输入、键、重试关系、素材、SVG 和 PNG 路径保持；原图像恢复规则照旧。
- version 2：全部必需映射必须存在且非 null。未知版本、损坏 JSON、缺失或 null 映射拒绝启动，不用空映射覆盖已有数据。
- 已有 state.json 始终先解码到零值结构并校验，不套用新库默认值掩盖缺字段。
- 升级/写入失败不能替换原文件或污染已提交内存；含嵌套切片的项目草稿和回执必须隔离复制。

mock-script running 在重启时恢复为 queued/0，保留同一个 attempt、来源和幂等关联，按至少一次语义重跑。queued 保持原输入；终态不重放。只有成功完成的一次原子提交才增加内容版本，重启后不能凭旧 running 状态重复叠加版本。

原图像 worker 的真实 running → failed/execution_interrupted、queued provider 绑定、expected_provider 原子守卫、错误脱敏、PNG 读写限制均保持；短剧不借用或修改这些语义。任一 worker 出现持久化故障仍应使服务停止，避免后台已失效却继续假装接受执行。

version 2 不保证可由旧二进制读取或安全降级；回滚应停服并恢复匹配版本的完整备份。现有文件仓库仍不提供多实例、分页、历史容量回收、整机断电数据库级事务或生产鉴权。本轮没有借新增流程改变这些限制。

## 前端同步边界

生成前必须先保存或放弃 title/brief 的本地修改；已有稿件再次生成应明确确认成功后替换当前稿、失败保留。编辑期间切换项目、空间或工作台须提示放弃未保存修改或阻止切换，不能静默丢稿。取消编辑只丢弃本地草稿，不取消后端生成。

请求失败和版本冲突保留输入；只有 API 成功响应才显示保存成功。项目或空间切换后，迟到响应不能覆盖当前界面；轮询终止或忽略旧目标响应。图像页面继续使用原能力守卫与独立素材/任务状态，短剧不能导致缓存图像 mock 状态越过付费保护。

## 验证计划与证据边界

架构角色维护 OpenAPI 与 [真实 HTTP 契约检查](../scripts/contract_check.py)，在临时数据目录和随机 loopback 端口启动当前后端，校验正常与 400/404/409 响应、成功回执重放、版本变化和失败保稿，并保留第三轮图像合同检查。测试不启用可发送外部请求的生产配置，异常退出路径清理进程和临时目录。

独立 QA 负责 Eino 实际执行、原子并发、深拷贝和失败写入、旧快照升级/损坏拒启、重启恢复，以及浏览器完整链路/迟到响应/草稿保护和图像布局回归。测试产生的剧本和 PNG 均为模拟或合成协议样本，不能作为真实模型质量、付费权限或企业隔离的证据。

2026-10-22，OpenAPI 通过 `openapi-spec-validator 0.7.2` 校验，并独立验证 20 个组件 schema、62 个示例及 101 个本地引用。项目解码器严格拒绝 null 的修复完成后，实际运行当前后端的合同检查通过 255 个 HTTP 响应校验（包含轮询，不是 255 个独立用例；响应数随轮询次数变化）。其中覆盖项目创建、需求更新、Eino 生成、手工保存、400/404/409、失败保留旧稿和来源、旧键返回当前投影、跨重启重放，以及图像缺凭据时仍独立生成剧本。

生产代码只读审查已核对：同锁中的回执/版本/活动生成检查顺序，输入与返回值深拷贝，领取前持久化 running，完成时复核项目与 base_version 并原子发布，独立 worker 的失败停服及取消退出，以及原图像保护路径保持。快照升级拒绝完整 v1 中混入非 null 新域映射，防止重置这类记录；缺失/null 的 v2 必需映射拒启。浏览器及进程中断等独立验收由 QA 记录，不以这些 API 证据代替界面验收。
