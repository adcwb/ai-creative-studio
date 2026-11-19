# 现状诊断 · 映创前端 UI 与长内容体验

审计对象：`frontend/`（React SPA）及其调用的 Go/Eino 后端接口契约  
基线提交：`fce862b9b98fc3cd8ab6d1cecb00d74597599052`  
审计日期：2026-11-19  
审计方式：静态源码审计 + 契约核对 + 历史截图佐证，未运行构建与浏览器  
口径：所有结论附源码位置；查不到证据的一律标注「未找到证据」，不作推测  

---

## 0. 执行摘要

前端的真实状态不是「antd 项目需要美化」，而是**一个手写 CSS 站点刚刚开始接触 antd**：127 个组件中只有 10 个引入 antd，原生 `<button>` 与 antd `<Button>` 的出现次数是 488 : 42。主题存在两套独立色源（CSS 变量与 ConfigProvider palettes），已出现至少 5 处色值漂移。

后端**完全没有流式输出**：前端零个 `EventSource`/`WebSocket`/`ReadableStream`，后端零个 SSE handler，Eino 编排走 `Invoke` 而非 `Stream`。因此「逐字渲染」类需求在当前契约下无法实现，只能做阶段式反馈。任务 `progress` 字段的真实语义是 0 / 35 / 100 三个**阶段标记**，而任务详情把它当百分比渲染成「任务进度 35%」——这是一处正在误导用户的语义错误。

长内容问题集中且可量化：全前端 `React.memo` 出现 **0 次**，剧本编辑器每敲一个字符会重建整部剧本、做两次全量 `JSON.stringify`、跑一次全量校验，并重渲染全部场景控件。18 个内容无上限的页面里，只有 4 个真正分页。大纲导航、滚动定位、URL 位置联动、页内正文搜索、吸顶操作栏、聚焦模式**全部为零**。

---

## 1. 工程基线

| 项 | 实测值 | 位置 |
| --- | --- | --- |
| UI 库 | antd 6.7.0 | `package.json` |
| 移动库 | antd-mobile 5.43.0 | 同上 |
| 框架 | React 19.2.0 | 同上 |
| 构建 | Vite 7.3.7 / TS 5.9.3 | 同上 |
| 路由 | react-router-dom 7.18.5 | `src/App.tsx` |
| 状态 | Context 沿 Outlet 逐层扩展 | `src/app/workspace.ts` |
| 虚拟列表 | 无 | 依赖清单 |
| 图标库 | 无第三方，自绘 25 个 SVG | `components/Icon.tsx` |

生产依赖仅 7 个，无 Redux/Zustand/React Query，无 CSS-in-JS，无 Tailwind。样式由 20 个手写 CSS 文件承担，`var(--…)` 引用 870 次、写死 hex 193 次；其中 99 次是 `tokens.css`（35）与 `themes.css` 变量定义行（64）里的令牌定义本身，**其余 94 处绕过令牌**。

路由为 `SessionGate → AppLayout → ImageWorkspace → 各页`，除 `ImageWorkspace` 外全部 route 级 `lazy`。`ImageWorkspace` 作为父路由承载创作草稿，既有路由规格明确要求不得拆分。

---

## 2. 视觉与品牌一致性

### 2.1 两套色源并存，且已经漂移

`ui/StudioTheme.tsx` 的 palettes 与 `tokens.css` / `layouts/themes.css` 是两份各自硬编码的色值表，没有派生关系。已确认的漂移：

| 项 | 组件库侧 | CSS 侧 |
| --- | --- | --- |
| 成功底色 | `#edf3e8` | `#edf3e5` |
| 控件边框 | `#7f8d78` | 手写普遍用 `#e0e5d9` |
| paper 占位符 | `#716254` | `#76685d` |
| screening 占位符 | `#b2b8bf` | `#a0a8b1` |
| Tabs 深色 | `#c3dce9` 写死 | 无对应变量 |

证据：`ui/StudioTheme.tsx:10-12,33`、`tokens.css:21,23,40`、`layouts/themes.css:4,14`。

后果是同一个页面里 antd 控件的边框明显深于手写控件，两套控件并排时观感割裂。另有 `--primary-contrast` 从未定义、仅靠 fallback 兜底（`features/prompts/prompts.css:3`、`features/workflows/workflow.css:35`），paper 主题未覆盖 `--error-*` 与 `--shadow-dialog`，`color-scheme: dark` 只在 screening 块内声明。

### 2.2 组件库覆盖率约 8%

antd 只出现在 10 个文件、12 类组件中；antd-mobile 只有 `ui/MobileSheet.tsx` 一处，且**没有自己的 ConfigProvider、没有引入其样式文件**，等于未接主题。

| 控件 | 手写 | antd |
| --- | --- | --- |
| 按钮 | 488 | 42 |
| 卡片 | 60 处 `.panel` | 0 |
| 表格 | 5 个 `<table>` | 1 |
| 进度 | 5 个 `<progress>` | 0 |
| 空状态 | 35 处 / 8 种写法 | 0 |
| 骨架屏 | 0 | 0 |

`src/ui/` 目前只有 4 个组件（StudioTheme / StudioDialog / StudioSelect / MobileSheet），**没有任何原子层封装**。错误态更分散：8 种 class、149 处，其中裸 `className="error"` 就有 142 处。

### 2.3 三套弹层、无 z-index 令牌

原生 `<dialog>` + `showModal()` 仍有 5 处（ImageResult、TaskDetail、PromptLibrary、ProjectPromptPicker、UsePromptDialog），与 `StudioDialog`（antd Modal）、`MobileSheet`（antd-mobile Popup）并存。`StudioDialog` 内部有 Layer 递增机制（基准 1000、每层 +100），但 CSS 里另有 30 / 29 / 20 / 1000 / 2 等写死值，其中 `style.css:263` 的语言反馈条与弹层基准同为 1000。

### 2.4 断点 15 种

CLAUDE.md 声称「断点统一 760px」，实测至少 15 个不同断点：760/761、700、600、640、720、900、480、1000、1050、1150/1151、1240、1600、1700。`ui/useMobile.ts:3` 用 760，而 `style.css` 同文件内还并存 700 与 600。

需要区分的是：可读性规格原文允许「共享壳使用 760px 边界，独立业务断点继续保留」，所以这不是单纯的违规，而是**共享壳与业务层没有划清边界**。

### 2.5 装饰性文案挤占信息层级

历史截图 `docs/screenshots/script-desktop.png`（2026-10-23，结构已与当前 `DramaScriptEditor` 核对一致）显示：剧本页在真正内容之前，先占据约一屏高度的是营销式标题「让故事，从这里开场。」、英文眉题 `A STORY STARTS WITH ONE IDEA`、侧栏装饰文案「让创意，从一张图开始。」与 `WORDS INTO STORIES`。

这些英文眉题是**硬编码英文常量、不走 i18n**（如 `DramaScriptEditor.tsx:27` 的 `THE CURRENT DRAFT`、`CANDIDATE / YOUR DECISION`），中文界面下也强制显示英文。它们与需求方「中文文案统一、杜绝空洞营销话术」的要求直接冲突。

同一张截图里，3 场戏的页面总高已达 2791px。按每场约 350px 线性外推，100 场将超过 35000px——这是「无上限长页面」最直观的量级证据。

---

## 3. 信息架构与交互痛点

**壳层是两栏，没有检查器位。** `components/StudioShell.tsx:52` 的 `.shell` 只有 226px 侧栏 + 主区，主区 `max-width:1680px`、左右 padding 44px。三栏只在页面内局部出现（`.creation-grid` 为 0.9 : 1.1 两列、`.detail-grid` 为等宽两列），都不是「大纲 / 主视区 / 检查器」结构。

**主操作沉底。** 「下一步：生成分镜」是 `ProjectScript.tsx:56` 的一个 `<Link>`，排在整篇剧本编辑器之后；「保存剧本」在 `DramaScriptEditor.tsx:42` 的最末尾；「保存项目简报」同样在表单下方。全站 `position:sticky` 只用于侧栏，**没有任何吸顶操作栏**。用户在第 80 场时无法一步触达保存或下一步。

**状态语义错误。** 后端 `progress` 只在三处写入：`store.go:258` 写 0、`:577` 写 35、`:622` 写 100，OpenAPI 明确描述「不是模型生成百分比」。而 `components/TaskDetail.tsx:57` 渲染 `<progress value={progress} max="100">` 并显示「任务进度 {progress}%」。用户看到的 35% 实际含义只是「已派发给上游」。

**错误信息被大量丢弃。** 后端错误信封有 9 个字段，前端 `ApiError`（`api.ts:29-34`）只保留 `message / status / code / field`，把 `request_id`、`correlation_id`、`category`、`suggestion`、`cause_known`、`upstream` 全部丢掉。后端已经给出的 `suggestion`（≤500 字的下一步建议）和 `category`（8 类枚举）没有被使用。

**错误码几乎没有文案。** `platform/diagnostics/codes.go` 的 knownCodes 共 171 个码，前端 locales 中**未找到任何错误码到文案的字典**。兜底文案是「服务拒绝了请求（{p0}）。请检查输入与当前配置。」，`{p0}` 直接塞原始英文码。全库只有 6 个码被特殊处理。生成历史更是把码原样渲染成 `<code>`（`GenerationHistory.tsx:22`）。

**参数快照有接口、UI 没消费。** 制作修订的 `manifest.sources.jobs[]` 真实保存了 prompt、prompt_source、director_context、visual_context、各类参考资产 ID 与 library_versions（OpenAPI `ProductionRevisionJobSource`）。但 `features/projects/RevisionWorkspace.tsx:14` 把 `jobs`、`contexts`、`library_versions` 全部声明为 `unknown[]`，等于放弃了这份快照。

---

## 4. 后端契约实测

这一章是后续所有「生成状态」UI 的地基，字段一律以实测为准。

### 4.1 没有流式输出

| 检查项 | 结果 |
| --- | --- |
| 前端 EventSource / WebSocket | 0 命中 |
| 前端 ReadableStream / getReader | 0 命中 |
| OpenAPI 中 event-stream | 0 命中 |
| 后端 SSE handler / Flusher | 0 命中 |
| Eino 调用方式 | `Invoke`，非 `Stream` |

Eino 编排是 `compose.NewChain(...).AppendLambda(...)` 两个 Lambda（`backend/internal/application/pipeline.go:116`），出口 `p.run.Invoke(ctx, in)`（`:126`），全库无 `eino/callbacks` 导入。

轮询由 `src/polling.ts` 统一调度：指数退避 `interval × 2^failures`，上限 30s；页面隐藏时暂停并 abort；4xx（除 408/429）立即终止。**没有重试次数上限，也没有总时长上限**。四个调用点中，`hooks/useWorkspace.ts:61`（1500ms）与 `WorkflowRunHistory.tsx:112`（2000ms）不返回终止条件，挂载即持续轮询。

### 4.2 状态枚举有三套，且不一致

| 实体 | 状态字面量 |
| --- | --- |
| 图像 Task | queued / running / succeeded / failed |
| CreativeJob | 上述 + cancelled |
| WorkflowRun | 上述 + cancelled + waiting_review + unknown |

图像任务**没有取消接口**（无 `/tasks/{id}/cancel` 路径），因此图像任务卡片上不能出现「取消」按钮。可取消的只有工作流运行。

其他不一致：前端 `Task` 缺 `updated_at`，而 OpenAPI 将其列为 required；前端 `features/workflows/types.ts:9` 的 `Run.status` 是裸 `string` 而非联合类型；前端 node result 声明了后端根本不存在的 `variable_name` 字段。

### 4.3 错误信封的完整形状

`error` 对象含 `code`、`message`（必填）、`field`、`request_id`、`correlation_id`、`category`、`suggestion`、`cause_known`、`upstream`。其中 `category` 枚举为 `internal | interrupted | upstream | storage | configuration | access | conflict | input`，`upstream` 含 `http_status` / `error_code` / `request_id`。

**这 8 个 category 是现成的错误分类维度**，不需要为 171 个码逐条写文案也能给出分层提示。

### 4.4 编排粒度：用户节点 ≠ 模型节点

工作流运行提供 `Run.Nodes map[string]NodeRun`，每个节点有 `node_id`、`status`、`job_id`、`result{text, asset_id, asset_kind}`、`error_code`、`reused_from_run_id`、`reviewed_by`、`reviewed_at`。节点 Kind 是用户画布语义（text / template / variable / llm / asset / image / speech / video / review / output），包注释明言模型工作委托给既有独立任务。

因此**可以做**用户定义编排步骤的步骤条与节点产物折叠查看；**不能做** Eino Chain/Graph 内部节点进度，也**拿不到** tool call 的结构化入参与结果（未找到证据）。可见的最细粒度是每节点状态 + job_id + 终态产物，2000ms 刷新。

### 4.5 分页能力矩阵

| 接口 | 分页 | 前端实际 |
| --- | --- | --- |
| `/tasks` | page_size 默认 4 | 已传 |
| `/assets` | 无 | 全量拉取 |
| `/library/assets` | limit 默认 20 | 只传 cursor |
| `/projects/{id}/generations` | 无 | 全量 |
| `/projects/{id}/history` | 无 | 全量 |
| `/workflow-runs` | cursor，无 limit | 游标翻页 |
| `/revisions` | limit 默认 20 | 只传 cursor |

`GET /assets` 的实现是遍历 `repo.Assets(space)` 后整体 `respond`（`backend/internal/application/http.go:102-125`），**后端侧就没有上限**。素材库的「禁止一次拉全量」在不改后端的前提下只能靠前端窗口化渲染缓解，无法真正减少传输量——这是必须如实说明的能力边界。

### 4.6 历史与版本已做到哪一步

已有：版本列表与恢复（带 `Idempotency-Key` 与 `expected_version`）、分支 fork 与游标翻页、**恢复前对比预览**（`GET /revisions/{id}/restore-plan` 返回 `changed` + `timeline` + `current_timeline` + `assets`）、节点级断点重跑（`restart_node_id` / `from_run_id`）、失败任务 resume、成功任务继续创作。

未有：任意两版自由 diff（restore-plan 只对比「待恢复版 vs 当前」）；参数快照的前端消费。

---

## 5. 超长页面清单与瓶颈

### 5.1 清单

| 页面 | 内容类型 | 量级 | 当前渲染 | 瓶颈 |
| --- | --- | --- | --- | --- |
| 剧本编辑 | 长文本+表单 | 场次无上限 | 全量 map | 每键重渲全剧 |
| 故事正文 | 长文本 | 多数组字段 | 6 处全量 map | 无折叠分片 |
| 系列工作台 | 卡片+正文 | limit 100 | 列表+预览全文 | 预览含全文 |
| 选角面板 | 表单卡片 | 角色数 | 全量 map | 历史全文展开 |
| 分镜编辑 | 表单卡片 | 镜头无上限 | 全量 map | 每镜 8+ 控件 |
| 生产板 | 媒体+表单 | 全量返回 | 23 处 map | 40+ useState |
| 媒体工作室 | 媒体+时间轴 | 全量 | 4 组 map | 无 memo |
| 素材库 | 图片卡片 | 后端无分页 | 全量 map | 无上限 |
| 资料库 | 卡片 | limit 20 | 单页 map | 翻页替换 |
| 任务列表 | 图片卡片 | 每页 4 | 分页 map | 已分页 |
| 生成历史 | 列表 | 后端无 limit | 折叠后全量 | 无分页 |
| 项目历史 | JSON 长文本 | 后端无 limit | 全量 pre | 无分页 |
| 动态分镜 | 媒体卡片 | limit 20 | 游标分页 | 已分页 |
| 数字人审片 | 视频+列表 | takes 数 | details 内 map | 视频未懒挂载 |
| 工作流历史 | 列表+节点 | limit 20 | 游标分页 | 已分页 |
| 提示词库 | 卡片 | limit 12 | 累积 map | 只增不减 |
| 模型管理 | 原生表格 | 全量 | tbody 全量 | 无分页 |
| 网关列表 | antd 表格 | 全量 | pagination=false | 无分页 |

18 个页面中真正分页的只有 4 个（任务列表、资料库、动态分镜、工作流历史）。全库 `.slice(0,n)` 只用于文本摘要与游标栈，**没有任何一处用于列表截断**。

### 5.2 剧本页每敲一个字符的真实代价

这是本次治理最有代表性的样本，链路完整如下：

`DramaScriptEditor.tsx:36` 的 textarea onChange → `sceneChange`（`:17`）用 `script.scenes.map` 重建**整部剧本** → `ProjectScript.tsx:55` 的 `editor.setScript` 写入全局 hook → `useDramaProject.ts:41-42` 的 `useMemo` 依赖 `script`，执行 `JSON.stringify(script) !== JSON.stringify(baseline.script)`，**两次全量序列化** → `DramaScriptEditor.tsx:15` 的 `scriptDraftError(script)` 全量校验 → 因为 `React.memo` 全库为 0，全部场景与对白控件重渲染。

编辑态挂载的受控控件数量约为 `2N + M + K + 1`（N=场次，M=对白行数，K=角色数）。按验收目标 100 场 / 2000 条对白估算，单次击键要重建 2000+ 个控件的 props、序列化两遍全文、跑一遍全量校验。

另有两处放大器：`renameCharacter`（`:24`）每次输入遍历全部 scenes × dialogue 做角色改名同步，是 O(N×M)；`ProductionBoard.tsx:31-69` 有约 40 个并列 useState，任一输入变更触发整页 23 处 map 重渲。

需要精确表述的是：**并非「整篇挂载一个超大编辑器」**，阅读态渲染的是 `h3`/`p`，只有进入编辑态才一次性挂载全部输入控件。问题出在「编辑态不分片」而非「阅读态有编辑器」。

### 5.3 key 稳定性与媒体加载

对白列表用 `key={lineIndex}`（`DramaScriptEditor.tsx:37`），删除中间一条会导致后续行状态错位；场景与角色的 key 是 `id ?? index`，旧结构项目（`structure_version < 2`）退化为索引 key。

全库 22 个 `<img>` 中 11 个有 `loading="lazy"`、1 个 eager、**10 个没有**。`aspect-ratio` 只有 4 处，素材网格与任务网格靠 intrinsic `width/height` 占位，`MediaStudio.tsx:204`、`LibraryEditor.tsx:96`、`ReferenceBindingsEditor.tsx:25` 连宽高属性都没有。9 个 `<video>` 均为 `preload="metadata"`，无懒挂载。

### 5.4 长内容能力现状

| 能力 | 现状 | 说明 |
| --- | --- | --- |
| 折叠展开 | 已有 | 44 文件用 details |
| 懒加载内容 | 部分有 | 4 处展开时才拉取 |
| 大纲导航 | 没有 | 现有 tab 是资料分类 |
| 滚动定位 | 没有 | scrollIntoView 全库 0 |
| URL 位置联动 | 没有 | location.hash 0 处 |
| 页内正文搜索 | 没有 | 仅素材/提示词服务端筛选 |
| 吸顶操作栏 | 没有 | sticky 仅侧栏 |
| 聚焦模式 | 没有 | 未找到证据 |
| IntersectionObserver | 无关 | 仅 3D 播放暂停 1 处 |

### 5.5 阅读排版

行高已达标（剧本 1.8–2.0），**字号未达标**：剧本正文与输入框均为 13px（`drama.css:6,53,56`），故事正文继承 14px。目标是 15–16px。

正文**没有阅读宽度上限**：`main` 的 `max-width:1680px` 是页面级约束，剧本正文容器无 max-width，宽视口下单行可超过 1300px。48rem/56rem 只用于交付与文案区。

对白是**单栏**：`.drama-dialogue` 为 grid，说话人 `<strong>` 独占一行、台词 `<p>` 在下一行，左侧一条 2px 竖线（`drama.css:54-56`）。没有说话人对齐列，扫读时无法快速定位某个角色的台词。

登场人物已经是独立区块（`.drama-characters`，两列 grid），但位置在正文之前而非顶部紧凑横条或右栏，每个角色占据一大块描述文字。

---

## 6. 缺失的可复用组件

按缺口严重程度排序：原子层（Button / Card / Tag / Progress / Empty / Skeleton / ErrorState）完全缺失；状态层（TaskProgress、生命周期徽标、错误分层提示）完全缺失；长内容层（三栏骨架、大纲、虚拟列表容器、页内搜索、吸顶操作栏、聚焦模式）完全缺失；领域层（ProjectCard、AssetCard、StoryboardCard、PromptEditor、ModelSelector）散落在各页，无统一入口。

现有 `RuntimeModelPicker`、`GatewayTable`、`ConfirmDialog` 已具备封装雏形，可作为领域层的起点而非重写对象。

---

## 7. 不能碰的既有决策

这些是审计中发现的、改造时最容易踩的雷，全部有原文出处。

| 雷区 | 出处 |
| --- | --- |
| 画布/遮罩/时间轴/原图查看器不迁 antd | ant-design-ui-design |
| 剧本候选采纳不得复活为主流程 | drama-script-usability |
| 图像历史每页 4 条 2×2 不得改 | image-history-actions |
| 网关列表不得全卡片化 | gateway-model-list-layout |
| 对比度 4.5:1 / 3:1 门槛不得降 | ant-design-ci-repair |
| 700px 等业务断点不得整体删 | interface-readability |
| ImageWorkspace 父路由不得拆 | frontend-routing |
| 缩略图 ≥190px 几何断言不得删 | ant-design-ci-repair |
| 品牌标记不得旋转拉伸加投影 | brand.md |
| 不加载外部字体 | brand.md |

另有两条与本次定调**直接冲突**，需要显式决策：三主题（森林/纸张/放映厅）在 `docs/design/2026-10-31-appearance.md` 有正式定义，且被三个 E2E 套件硬编码断言；品牌「暖白承载创作内容」的定位句与整套对比度核算数值都建立在浅色之上。

---

## 8. 本轮未能验证的事项

未运行 `npm run build`、`tsc`、Playwright 与 Go 测试（VM 内无 Go 工具链，`backend/bin/server.exe` 为 Windows 二进制无法在此执行），因此**没有本轮实测的性能数字、没有当前 UI 的新截图**。第 2.5 节引用的截图摄于 2026-10-23，早于 antd 迁移，仅用于结构与信息层级佐证，其视觉细节不代表当前状态。

react-virtuoso / TanStack Virtual 与 React 19.2 的兼容性、antd 6 的 `theme.components` 对深色算法的覆盖完整度，均需在实际实施第一步时实测确认，本轮未验证。

「100 场 / 2000 条对白」的渲染代价是基于代码路径的推算，不是实测帧率。真实数字将在底座落地后用固定的合成数据集测得并写入验收记录。