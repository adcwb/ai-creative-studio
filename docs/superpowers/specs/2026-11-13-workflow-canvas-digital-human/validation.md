# 本地阶段验收

2026-11-13。基线 `486ef47`，实施分支 `feat/workflow-canvas-digital-human`。本阶段未推送、未部署、未执行生产迁移；上一阶段云端 CI 43475422718 的25项成功只适用于486ef47。

## 已执行

| 检查 | 结果与实际证明 |
| --- | --- |
| 后端 `go test ./... -count=1` | 通过；application 247.944s。此轮全量使用文件环境，依赖显式PG/外部环境的测试按既有条件跳过，不冒充全量PG或真实模型验收；后续修正以受影响专项复验。 |
| 工作流/数字人应用与存储竞态专项 | 通过；涵盖不可变修订、CAS、回执不倒退、人工等待/继续、当前权限、取消/拒绝、真实PG双控制器和审片竞争、文件多实例并发、损坏拒绝覆盖、迁移往返。 |
| 最新工作流恢复专项 `-race` | 通过，59.237s；包含已提交子任务与流程文档之间的崩溃恢复、配置撤下后只读找回原结果、并行失败停止新接纳并核对已接纳结果。 |
| PostgreSQL 独立 AuthStore 与控制器专项 `-race -run '^TestPostgresWorkflow'` | 通过，16.875s。使用本机一次性PG17 schema；当前成员撤销在另一权限存储实例重新读取并拒绝，双控制器与并发审片CAS、离线导入导出保留暂停运行/私有选择/原回执。 |
| 图领域与工作流文件存储整包 | 通过；环/类型/未配置/重复输入/孤立模型节点/非有限坐标/任意代码节点拒绝；锁、原子失败、重开、复制验证、租户范围与损坏拒绝。 |
| 原生数字人 files-v1 | 33秒且输入超过10MiB的配音经测试执行器、全解码、授权声音替换、合成和入库通过；越界清单、无效视频与超时拒绝且清理；错误分类显式断言。测试程序仅产生合成色块，不是人物模型。 |
| 画布真实协议浏览器 | **6/6通过**（桌面3、手机3）：拖动/保存/重载修订、人工确认、图片实际网关请求、响应丢失同键同body恢复、一次上游调用；配音实际 speech 请求、原文/音色参数、试听确认、音频输出。无真实付费调用。 |
| 完整路由浏览器 | **134通过，2跳过，0失败，7.2分钟**；跳过为既有桌面专用断点/侧栏图标检查在手机设备跳过，桌面执行。原90项运行曾87通过/2跳过/1超时；修正冷加载和拆分检查后扩为136项，断言及超时未削弱。两次本机负载不同，不能当云端提速基准。 |
| 前端单元 | 25文件、80项通过；含新连线类型/环/重复端口和模板无假结果。 |
| 前端 Lint、类型检查、生产构建、国际化 | 通过；按需加载的作品列表不再在打开列表时加载全部制作/剪辑/发布面板；画布独立懒加载。 |
| OpenAPI 校验 | `openapi_spec_validator` 通过；新增10条路径/12个操作和13个schema，旧定义语义未改变。 |
| Go格式、vet与Linux/Windows命令构建 | 通过；Windows为amd64交叉编译，不能代替Windows本机运行。 |

本地日志位于 `/tmp/canvas-*.log`、`/tmp/workflow-*.log`、`/tmp/avatar-*.log`，详细用例在 `workflow_test.go`、`digital_human_files_test.go`、`workflowrepo/store_test.go` 与 `frontend/e2e-gateway/workflows.spec.ts`。测试目录自动清理会移除运行 trace，不能以瞬时文件作为永久生产证据。

## 截图实查

- 画布1920×1080桌面、390 CSS像素手机；forest/paper/screening三主题桌面。手机名称输入框曾被工具条挤窄，已修正并增加实际宽度断言。
- 数字人口播1920×1080桌面和手机；明确形象授权、许可未确认、长于旧30秒的配音选择、原请求恢复和任务状态。
- 本机证据副本：`/workspace/artifacts/workflow-canvas-digital-human/`。截图使用合成账号与素材，不含真实凭据或生产数据。

## 未验收或未实现

- 新画布的视频节点已接既有CreativeJob能力，但尚无画布专属视频异步端到端用例；不能把既有供应商底层测试称作该节点的真实供应商验收。
- vars/LLM文本节点、局部节点复用、专用云数字人协议/分段恢复、独立可撤销声音授权、实时与3D，仍见 tasks.md；它们是代码工作未完成，不应全部归咎于缺少真实凭据。
- 没有真实模型、数字人权重、许可、GPU、S3、1K–4K质量、真实Windows文件执行器或生产环境验收。本轮无付费授权，也无许可已确认的真实引擎；因此这些外部验收**未执行**，不是测试通过或被审核拒绝。
- 没有运行本轮云端CI、镜像发布或容器验收，因为第1/4项按当前授权保留本地。未将此前486ef47的绿色状态套给新代码。
- 第6–10项还存在整项目/跨集回退、影响图与艺术一致性、整季交付、多集发布治理、所有权转移/团队解散/可信邀请/订阅等缺口，详见相邻 remaining-gaps.md。

## 后续整合与增量

创作者补齐阶段及CI修复已整合，新增模板/视口/视频节点实测和权限测试。以上表格保留原4cf33ad阶段事实；当前精确结果及未完成边界见 [整合增量](integration.md)。桌面/手机新增协议8项及模板操作2项通过，不等于真实模型或完整数字人规格全部完成。


## 2026-11-14 运行输入、局部重做、声音授权与检查点

本地增量合同见 [runtime-recovery.md](runtime-recovery.md)。以下均为隔离合成数据与本机环境，不是供应商或商业许可验收。

| 检查 | 精确结果与边界 |
| --- | --- |
| 后端文件模式完整 `go test ./... -count=1` | 通过，application 183.184s；最新NUL声明防护之后另跑领域竞态通过，未把旧全量称作包含此单行防护 |
| 文件+真实PG `-race`，DigitalHuman/VoiceAuthorization/Workflow/BlobMigration/相关PG恢复 | 通过，application 126.455s，含跨实例授权撤销、检查点恢复、不确定结果不重派、授权/证明迁移 |
| 集纲实际网关固定来源及显式CAS采用 `-race`，文件+真实PG | 通过4.933s，验证内部CreativeJob getter仍保留私有运行选择 |
| workflows/prompts/workflowrepo全包 `-race` | 通过1.027/1.023/1.079s，含无效默认声明即使被输入覆盖也拒绝 |
| `go vet ./...`、gofmt、diff whitespace | 通过；Windows amd64 server/worker交叉编译通过，不等于原生Windows运行 |
| 前端单测 | 26文件83项通过；类型、lint、构建通过；i18n 3221消息与OpenAPI校验通过 |
| 运行变量→字面模板→真实聊天协议→人工确认→显式局部重做 | 桌面1920×1080/手机两项通过31.9s；校验模型选择器、真实请求正文与两次准确派发、同键同body丢响应恢复、成功上游复用、输入改变409、跨空间403 |
| 原画布/图像/配音/异步视频/模板回归 | 桌面5项通过40.4s、手机5项通过41.7s；每设备新后端保留原注册防滥用与全部原断言 |
| 数字人授权/未知请求/恢复界面 | 桌面/手机两项通过36.0s；声音授权创建撤销、新生成禁用、刷新恢复原提交及原resume键/body。路由合成fixture不代表真实模型 |

回归中发现并修正：HTTP脱敏错误地置于内部getter导致集纲丢失运行选择；已将脱敏限定HTTP边界并通过集纲回归。架构检查为复用纯prompts词法新增单一workflows→prompts显式依赖，不开放基础设施依赖。新增实际属性栏测试发现LLM模型选择器漏接线，修正后双设备通过。截图发现确认框受全宽输入样式拉开，局部修正后双设备复验通过，未删除断言或绕过安全。

截图已实看，保持森林主题、现有表单和手机单栏，无横向页面溢出：`/workspace/artifacts/workflow-canvas-digital-human/followup/` 下的 `workflow-runtime-viewport-desktop.png`（1920×1080）、`workflow-runtime-remake-desktop.png`、`workflow-runtime-remake-mobile.png`、`digital-human-desktop.png` 与 `digital-human-mobile.png`。手机为390 CSS像素/3倍截图；原生控件文案随浏览器语言，视觉测试未证明所有浏览器。

日志保存在该环境 `/tmp/creator-followup-full-file-final.log`、`creator-followup-postgres.log`、`workflow-outline-final-pg.log`、`workflow-domain-final.log`、`workflow-runtime-visual-final.log`、`avatar-recovery-browser.log`、`workflow-followup-front-check.log`。未执行新分支云端CI/镜像、生产迁移、真实S3、真实GPU/权重/付费模型、原生Windows、商业授权或艺术4K验收；本轮不推送。
