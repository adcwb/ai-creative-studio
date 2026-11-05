# 创作交互与实现设计

状态：Implementing，2026-11-05修订4；本文保留完整目标合同，当前子集见[实施记录](implementation.md)。UploadSession、S3及持久渲染核心已分阶段交付，见[渲染实施记录](render-implementation.md)；系列实体等仍未实现。60–120秒/集、用户可选1K–4K和项目不管理额度是已明确约束；不代表代码已满足。当前能力见[代码证据](research.md)，需求编号见[需求规格](requirements.md)。

## 1. 从创作判断出发

### 1.1 设计师：从灵感到自己的配方库

图像入口保留当前描述与模型选择，模板区增加“内置 / 我的 / 组织”切换、搜索、类别和标签。卡片显示名称、意图、比例、来源和版本；预览图为用户明确选择的真实素材，没有封面时用中性占位图，不能捏造生成效果。

“存为模板”从当前草稿/结果打开抽屉：先填名称与用途，再调整正文、比例和风格；默认个人可见。保存后回到原创作，不生成、不清空草稿。内置模板点“复制改版”后进入同一编辑器，用户不需要先懂变量语法。

P2 的高级区按“主体、场景、光线、构图、色彩、禁改项”提供可选编写提示，不强迫套固定审美公式。用户可把变化部分提取为变量，再查看最终文本。例：`{{subject}}，{{palette}}色调，柔和侧光，右侧留白`；主体必填，色调是可枚举选项。编辑器解释变量替换位置，未知变量就地提示。

选用进入当前创作草稿，展示“来自 XX v3”，变量预览不调用模型。用户可以脱离配方继续改写；记录标为“基于 v3 修改”，不谎称仍与模板完全一致。从满意结果保存新配方时可保留原来源，避免反复覆盖团队原版。封面/参考是版本化素材，不缓存任意外部 URL。

### 1.2 编剧：先规划整季，再逐集写好

系列首页提供三个核心视图：“故事圣经”“季与分集”“连续性”。圣经记录主题/受众/世界规则、角色欲望—弱点—转变、关系和视觉基调；季视图呈现主线阶段、关键转折、伏笔埋设与回收；分集卡片记录本集目标、开场悬念、冲突、情绪转折、结尾钩子与目标时长。

这些是写作工具，不是要求每一集重复同一种结构。AI 给出多个候选，编剧可选择、组合或重写。选择 5–10 集范围生成集纲时，只创建该范围候选，不把全部 200 集剧本塞进一个上下文。

剧本工作台左侧为季/集索引，中间是当前集的场次与对白，右侧是相关角色、设定、伏笔和连续性发现。事实区分“正式设定 / AI 建议 / 有意误导”；角色所知与观众所知分开。每条发现能回到具体集、场景、版本与文本证据，采纳后才写入正式资料。

### 1.3 导演：以场面目标组织镜头

导演先回答“这场戏改变了什么、观众跟谁看、何时给出信息”，再安排全景/中景/反应镜等镜头；字段允许角度、构图、轴线、视线、站位与动作、摄影机运动、对白/环境音/音乐意图。原有 purpose/performance/continuity 字段保留并映射，能力不足的字段保留为创作意图而不是丢弃。

每镜先做静帧与可替换临时声轨，合成动态分镜来判断对白长度、停顿、反应和剪辑节奏。此阶段确认的是叙事可读性，不能说明正式视频动作已合格。只有选定镜头进入正式素材生成；静帧漫剧可直接使用确认图片与显式轻运动，视频镜头使用实际 video 能力。

镜头视图延用候选条、左右比较、采用与时间点批注；新增“与角色定稿对照”“上一镜 / 下一镜”查看。采用后明确选择“应用到剪辑”，避免生成结束覆盖导演已经排好的时间轴。返修单说明问题、目标、保持不变的内容及责任人，完成候选不自动解决原批注。

### 1.4 剪辑与制片：能交付、能返工、能接着做

系列制作矩阵按集显示剧本/分镜/预演/图片/声音/剪辑/审片/交付状态，展开到具体阻塞。默认只取摘要和缩略图，进入集才加载编辑数据。手机改为单集卡片与分步页签；键盘可以选片、切集、打开批注，未保存内容跨集提示。

批量操作先选集段和阶段，列出任务数、模型、依赖与可执行项，再启动一次；整季也可选，不设20集一单的限制。采用1–2分钟集长时，编剧先安排开场问题、冲突升级、转折/情绪落点和结尾钩子；导演按对白、表演停顿与反应镜决定镜头数量，不为凑时长机械生成30镜。资源繁忙显示排队原因；无模型只阻塞对应节点，用户仍可写作、上传合法素材和手工剪辑。无需填写价格或预算。

审片完成后创建封版清单，正式成片永远指向确定版本。改稿产生新修订，旧封版仍可下载、比较与回滚查看。社交发送继续使用原平台授权与回执合同，不能把封版或导出自动当作发布授权。

```mermaid
flowchart LR
  Bible[世界与人物定稿] --> Outline[季结构与集纲]
  Outline --> Script[逐集剧本候选与采纳]
  Script --> Board[导演镜头与动态分镜]
  Board --> Make[逐镜素材 声音与候选]
  Make --> Edit[选片 剪辑与审片]
  Edit --> Seal[封版与导出]
  Edit --> Change[返修与影响清单]
  Change --> Script
  Seal --> Delivery[显式平台交付与状态查询]
```

这些节点表示人和系统的工作成果，不是新增必须逐级审批的行政流程。单人可以在同一工作区完成采纳。

## 2. 架构与模块边界

保持 Go 模块化单体、React SPA、现有 PG 队列和 BlobStore。新增模块仅在阶段实施时创建，不为设计建空代码目录。

| 拟定目录 | 责任与复用 |
| --- | --- |
| `backend/internal/modules/prompts` | 纯模板语法、变量编译、版本摘要与可见性规则；不发 HTTP |
| `backend/internal/modules/series` | 系列/季/集、正式设定、事件/伏笔与连续性规则 |
| `backend/internal/modules/productionruns` | 有界节点依赖、状态转移、attempt/输入签名与失效分类 |
| `backend/internal/persistence/promptrepo`、`seriesrepo`、`productionrunrepo` | 空间限定的 SQL/文件仓储、CAS、幂等回执；不引入新整体 scope |
| `backend/internal/application/prompt_*`、`series_*`、`production_run_*` | HTTP、鉴权、事务与现有 Project/worker 协调 |
| `frontend/src/features/prompts`、`features/series` | 独立列表、编辑器与系列矩阵；通过现有 api/i18n/路由/草稿机制接入 |

现有 `library` 保存角色/场景/商品，新增风格与声音类型需单独扩展；`projects` 管每集剧本与分镜；`media` 管该集素材与剪辑；`revisions` 现有纯规则可复用摘要、分支 CAS 与边验证，需先补齐应用/仓储合同才称为完整修订系统。业务域不能反向导入 application 或供应商 SDK，更新架构依赖测试。

## 3. 提示词领域与 API

### 3.1 拟定数据

| 实体/建议表 | 关键字段 | 不变量 |
| --- | --- | --- |
| PromptTemplate / `studio_prompt_templates` | `space_id,id,owner_id,visibility,name,description,category,tags,status,head_version_id,row_version,created_at,updated_at` | visibility=`private/organization`，身份稳定；共享是显式元数据更新 |
| PromptVersion / `studio_prompt_versions` | `space_id,id,template_id,number,syntax,body,variables,defaults,reference_bindings,notes,sha256,created_by,created_at` | 只追加；syntax=`plain/variables_v1`；旧纯文本中的花括号不自动解释 |
| PromptFavorite / `studio_prompt_favorites` | `space_id,actor_id,template_id,created_at` | 用户私有偏好，不修改团队版本 |
| PromptReceipt / `studio_prompt_receipts` | `space_id,actor_id,operation,key,digest,result_id` | 同键同输入重放，同键异输入 409；重放先验证当前访问权 |
| PromptSource（嵌入任务） | `template_id,version_id,variables,compiled_sha256,effective_sha256,edited,reference_versions` | 保存最终输入与来源；重试不读取新 head |

索引：模板 `(space_id,visibility,status,created_at,id)`、`(space_id,owner_id,status,created_at,id)`；版本唯一 `(space_id,template_id,number)`；所有跨表关系包含 space_id。首版参数化 substring 查询并转义 LIKE 通配符，结合可见性条件与 keyset 分页，基准不达标才引入专用全文索引。不得先查询全文再在前端过滤私人结果。

拟定初始字段合同：名称1–80字、简述≤500字；普通图像接口目前正文/展开后3–1000字，模板首版遵从这一请求合同，若扩展须同步实际模型context与API版本，不能伪装成账户剩余额度。变量名为`[a-z][a-z0-9_]{0,31}`，变量值/enum/标签采用纯文本且受当前请求的解析字节预算保护，不另设累计数量配额。参考配方可保存多份版本引用并分页，实际派发依据模型真实参考数量/组合能力选择，不能把模型一次最多4张变成配方库只能保存4张。类别可空，未知类别不伪装成服务器创作类型。

个人模板正文只给当前空间内仍有权限的所有者；组织模板按成员/编辑权授权。共享改变可见性时提示所有历史版本将随之公开给组织；若只想分享当前配方，复制为新的组织模板，来源只记录不泄露私有正文。私有模板用于组织可读任务时，必须提示最终提示词会进入任务历史；来源模板的私有权限不意味着生成记录也私有。模板改回私有不能撤回已被合法引用的任务快照。

### 3.2 拟定 API 合同

沿用会话、空间解析、CSRF/写权限和 `Idempotency-Key`（现有格式）的模式。P1纯文本路径现已落地（含版本追加可原子更新metadata/expected_row_version）；变量和参考字段仍属后续阶段。所有路径包含当前授权空间的解析，不信任客户端 actor_id。

| 方法与路径（前缀 `/api/v1`） | 输入/输出与行为 |
| --- | --- |
| `GET /prompt-templates` | `q,category,tag,source,status,cursor,limit`；source=mine/organization，默认 active，返回 items/next_cursor；默认20、最大100；游标绑定空间、调用者、过滤条件 |
| `POST /prompt-templates` | metadata + initial version；201 模板和版本，不入队 |
| `GET /prompt-templates/{id}`、`GET /prompt-templates/{id}/versions` | 当前可读元数据/分页版本；不可见按404处理 |
| `GET /prompt-templates/{id}/versions/{versionId}` | 明确历史版本；相同租户和 visibility 校验 |
| `PATCH /prompt-templates/{id}` | expected_row_version + metadata/visibility/status；200，冲突409；不改历史正文 |
| `POST /prompt-templates/{id}/versions` | expected_head_version_id + 新配方；201，事务内追加并推进 head |
| `POST /prompt-templates/{id}/copies` | 可读版本 + 新名称/visibility；201 新身份、新版本；不复制原作者权限 |
| `PUT /prompt-templates/{id}/favorite` | `{enabled:boolean}`；200当前偏好，幂等写入 |
| `POST /prompt-templates/{id}/preview` | version_id/variables，200展开文本、计数、摘要、能力需求；无模型调用/无持久任务 |

创建请求示例（P2，字段有意省略时间与用户身份）：

```json
{
  "name": "柔光人物海报",
  "description": "右侧留白，用于人物概念探索",
  "visibility": "private",
  "category": "人物海报",
  "tags": ["柔光"],
  "version": {
    "syntax": "variables_v1",
    "body": "{{subject}}，{{palette}}色调，柔和侧光，右侧留白",
    "variables": [
      {"name":"subject","type":"text","required":true,"max_length":120},
      {"name":"palette","type":"enum","options":["暖米色","冷灰"],"default":"暖米色"}
    ],
    "defaults": {"kind":"poster","ratio":"4:5","style":"illustration"},
    "reference_bindings": []
  }
}
```

已有 `/templates` 保持内置只读；新列表独立加载，失败不让内置模板和自由输入同时不可用。实际生成继续走现有 tasks/creative 接口，扩展可选 `prompt_source`；服务端重新读取可读版本、编译、验证参数并保存来源快照。`edited=false` 时最终文本必须与服务端编译一致，`edited=true` 时允许用户改写，记录原编译与最终输入两个摘要。无来源的旧请求行为保持。

归档后不能再作为新配方选用；已有任务的显式重试从服务端保存的原任务快照读取，不重新编译归档模板，也不信任客户端伪造“历史来源”。必须有原任务当前访问权，并重新校验实际模型、素材和审核条件。模板共享权限后来收紧时，旧任务可读性仍由任务/项目权限决定，界面不提供越权打开原模板的链接。

配方的 defaults 只预填，用户最终参数与选择覆盖它；model ID 不写进配方的可执行默认值。参考绑定复制入当前项目时按现有库引用归属/类型规则处理，跨组织拒绝。首页暂不支持引用的配方显示“在项目制作中使用”，不能悄悄丢掉参考。

错误沿用问题响应合同，拟定 code：400 `prompt_variable_missing/unknown`、`prompt_expanded_too_long`、`invalid_prompt_parameter`；403 当前写入无权；404 不可见或不存在；409 `prompt_version_conflict/idempotency_conflict/prompt_archived`；资源忙等待调度，不用429作为累计配额；503 存储/审核/模型配置不可用。错误包含可定位字段，不回显私人正文或凭据。

## 4. 系列、剧作与资产数据

### 4.1 核心实体（均为新增目标）

| 实体/建议表 | 字段与约束 |
| --- | --- |
| Series / `studio_series` | `space_id,id,title,logline,audience,format,status,head_bible_version_id,row_version` |
| Season / `studio_seasons` | `space_id,id,series_id,ordinal,title,arc_revision_id,row_version`；同系列 ordinal 唯一 |
| Episode / `studio_episodes` | `space_id,id,season_id,ordinal,title,outline_revision_id,project_id,target_duration,status,row_version`；同季 ordinal 唯一；新集 project_id 在空间内唯一 |
| BibleVersion / `studio_series_bible_versions` | 世界规则、主题、角色弧/关系、风格/声音绑定、禁改项、正式事实版本；不可变 |
| StoryFact / `studio_story_facts` | `series_id,id,version,kind,subject_id,value,story_time,valid_from/to_episode_id,knowledge_holders,evidence_refs,status`；区分 objective/subjective/unconfirmed |
| StoryEvent / `studio_story_events` | 发生时间、叙事出现位置、角色/道具状态变化、原始修订引用 |
| Foreshadow / `studio_foreshadows` | 埋设/回收集与证据、状态、意图；不得仅靠一段模型摘要代替 |
| ContinuityFinding / `studio_continuity_findings` | 基于哪些版本、具体证据、建议、severity、confidence、处理决定；模型发现不自动成为事实 |
| ContextSnapshot / `studio_story_contexts` | 本集相关正式资料、来源版本/摘要、纳入/省略清单、compiler_version；可复现 |
| EpisodeRelease / `studio_episode_releases` | 集 ID、序号快照、完整 revision manifest、渲染设置、审片记录、artifact IDs、发布回执关联；只追加 |

所有唯一约束、外键和查询均包含 space_id；有序分页使用 `(ordinal,id)`。批量重排在季版本 CAS 的一个事务里完成，不以显示集号作为事实/镜头主键；伏笔区间通过稳定集 ID 和保存的顺序修订解析。禁止删除有引用的实体，归档保留历史。正文大字段/素材不进入系列矩阵列表。

### 4.2 一集一项目与旧多集兼容

实现校准（修订10）：系列存储基础阶段使用 `studio_series_nodes` 的每实体独立行与类型列承载上述 Series/Season/Episode，`studio_series_receipts` 保存原操作回执；不是整体scope或只扩大旧项目集数。复合外键约束空间/父类型/父ID/系列归属，同父级编号为延迟唯一约束。Project的只读 `series_episode_id` 关联独立剧集，目录与Project使用相同不透明ID便于直接定位。PG同事务创建；文件使用 preparing→Project落盘→ready 的持久交接，重启恢复元数据且不调用模型。该基础阶段尚无系列HTTP/前台，输出意图与外部集号尚待接入，见[实施记录](series-storage-implementation.md)。

新 Episode 通过 application 同一事务创建 Project 并关联，现有 Project 编辑器复用。Project 内本地剧本/时间轴集号仍为1，**外部季/集序号由 Episode 提供**；UI 不把本地1显示成所有集都是第1集。系列导出入口保存 `episode_id + ordinal_snapshot + project_local_episode=1`，旧项目导出接口继续其旧语义。

旧项目继续显示原 episode_number 与 episode_timelines；不能把旧项目第7集直接链接成新第7集却调用本地1。显式迁移预览列出来源项目、来源集号、场景/镜头/素材及导出；按选定集建立独立目标项目，并记录 `(source_project_id,source_episode_number,target_episode_id,target_project_id,migration_id)`。旧记录与成片保持原来源。

共享旧素材必须经现有库引用或授权复制成目标项目拥有的素材；Blob 可按内容摘要去重，逻辑归属和引用计数独立，不能靠 URL 别名绕过跨项目校验。未使用素材留在旧项目；未知集归属的独立镜头进入迁移人工确认列表，不擅自猜测。迁移完成前不把 legacy view 计为已满足独立生产能力。

### 4.3 稳定 ID 与创作版本

ScriptCharacter 增加 id 和可选 library_asset/version；Dialogue 增加 character_id，显示名仍保存作快照。Scene 增加 id，StoryboardShot 增加 scene_id；新增结构合同版本，旧名字/scene_index 经确定性映射转换。ID 由保存的迁移映射产生，不在每次读取随机生成；场次重排、角色改名不改变关联。找不到、重复或越界引用拒绝迁移，保留原件。

2026-11-05实现状态：稳定ID、原映射、显式升级及来源分镜为T32A已实现，接口和验证见[稳定关联记录](structure-implementation.md)。角色库版本、跨集连续性、旧多集实际拆分和后续修订依赖仍为计划，不能把本节全部设计视为已完成。

基于现有 revisions 纯规则增加持久 EpisodeRevision / Branch / DependencyEdge。Manifest 必须包含剧本、分镜、时间轴、字幕、资产/声音版本、模板来源、context 和父修订摘要；不能把认证、费用、运行时权限或发布令牌放进可恢复内容。恢复创建新修订并保留并发时钟，模型权限重新校验。

角色状态如伤口/服装/道具归入 story event 或该集 binding override，不通过更新全局资产版本来偷偷改早期集。新风格卡保存色板/线条/构图说明与参考；声音卡只保存有许可的 voice reference/notes，真实密钥仍在网关凭据系统。原 character/product/scene JSON 兼容读取，不强制补假声音。

## 5. 制作编排与真实模型

### 5.1 拟定实体与派发事务

`ProductionRun(space,series,scope,source_revision,plan_digest,status)` 聚合有界范围，节点分页；`RunNode(id,episode_id,shot_id,kind,input_digest,dependencies,gate,status)` 表示逻辑成果；`NodeAttempt(id,node_id,number,input_snapshot,model_selection,dispatch_receipt,queue_id,state,outputs,error)` 表示一次具体执行；`ReviewDecision` 保存人对具体修订/候选的采纳。建议表分别为 `studio_production_runs/nodes/edges/attempts/review_decisions`。

依赖图只允许预定义阶段和无环边，禁止用户运行任意脚本。一个run可以选择整季/任意明确范围；建议默认先做小批，但没有最多20集或最多4候选的业务限制。候选默认1份，用户主动增加后后台逐页展开节点；单个模型若每请求只支持n=1，拆为多个独立attempt，不能绕过上游额度。大图用惰性物化、分页和节点游标，不把全部提示词塞进queue payload。

一次调度事务锁定 ready node，检查 run 状态和当前授权，插入唯一 `(space,node_id,attempt_number)`、输入/配置版本快照及 execution_jobs/outbox；事务提交后才允许网络派发。worker 复用现有非财务 dispatch receipt、租约和 fencing；输出先入 Blob，再在有效租约和预期版本下原子记录，失败的孤儿 Blob 由保留期清理。旧队列接入跨库事务不得伪装原子，当前同 PG 事务优先。

### 5.2 状态合同

节点状态：`blocked → ready → queued → running → awaiting_review → accepted`，失败可为 `failed/unknown/canceled`；`stale` 是内容版本失效标记，不能把已存在成功结果抹成失败。stage gate 由艺术采纳产生，单人可以直接采纳；自动化阶段仅对显式启动的依赖继续推进。

Run 状态：`planned/running/pausing/paused/canceling/canceled/blocked/completed`。暂停是禁止新派发，已发送请求继续记录；取消之后回来的成功结果保存为“取消后完成，未采用”，不会推进下游。发起取消与派发边界通过同一事务/锁顺序串行化，不能用 UI 切按钮替代。

已发送但结果未知：保持 unknown 和上游 ID，允许实际适配器支持的只读恢复查询；不支持查询则给出明确人工处理路径。不得重启后自动重发 POST。已确认未派发的暂态内部错误可以退避重试，确认拒收的上游限流也必须按已核验合同分类；默认不能把任意5xx归成安全重发。

用户显式重试创建新 attempt，保留旧 attempt 的潜在迟到结果与重复风险提示；迟到结果只能作为候选，不能覆盖新采用版本。修改剧本/资产后影响分析产生 `must_regenerate/review_needed/unaffected`，由人选择重新制作范围；角色身份、禁改项等风险不能自动判为无影响。

### 5.3 能力路由

| 阶段 | 实际所需能力 | 不可替代边界 |
| --- | --- | --- |
| 集纲、剧本、分镜建议、文字连续性检查 | text + 结构输出合同 | DeepSeek 等文字配置不承担图片生成 |
| 镜头图/封面/局部图片编辑 | image + 参考/遮罩等细分支持 | 不能静默去参考或返回演示图 |
| 视觉差异辅助检查 | vision + 对应图片输入 | 不是人脸身份验证 |
| 图生视频/视频镜头 | video + 首尾帧/引用支持 | 文本/image 能力不能假充 video |
| 配音、转写、字幕对齐 | tts / transcription 或已支持本地工具 | 有声音授权与当前能力才启用；不用假音频补齐 |
| 静帧预演/剪辑/导出 | 现有 FFmpeg/媒体工具 | 明示为合成预演，不能称视频模型输出 |

复用功能绑定和实际适配器能力检查。新增“集纲/连续性”功能绑定须更新服务端目录/OpenAPI/UI；不借用现有绑定却隐藏其用途。每次派发重新检查成员、模型版本、引用权限和审核，旧 snapshot 是证据而非永久授权。绝不恢复报价、预算或成本预留合同。

## 6. 系列和编排 API（拟定）

所有写接口沿用幂等键、预期版本和空间授权；异步创建202，普通创建201、更新200；不可见404、写无权403、无效400、版本/幂等/失效冲突409、资源429、依赖不可用503。列表默认20/最大100，统一返回 items/next_cursor。

| 路径（前缀 `/api/v1`） | 用途 |
| --- | --- |
| `GET/POST /series`、`GET/PATCH /series/{id}` | 系列列表/创建/元数据 CAS |
| `GET/POST /series/{id}/seasons` | 季列表/创建；重排 `POST /series/{id}/season-order` |
| `GET/POST /seasons/{id}/episodes`、`GET/PATCH /episodes/{id}` | 分集分页/创建与当前 project_id；重排 `POST /seasons/{id}/episode-order` |
| `GET/POST /series/{id}/bible-versions` | 正式世界/人物资料版本；追加需 expected_head |
| `GET/POST /series/{id}/facts`、`GET/POST /series/{id}/events`、`GET/POST /series/{id}/foreshadows` | 分页检索/增加版本；旧版本不覆盖，head 推进用 expected_version |
| `POST /episodes/{id}/context-previews` | 构建上下文快照预览/缺失报告；无收费调用 |
| `POST /episodes/{id}/outline-generations`、`POST /episodes/{id}/continuity-checks` | 显式启动真实 text 任务，202；输入 source revision/模型选择，候选另行采纳 |
| `GET /episodes/{id}/findings`、`POST /episodes/{id}/finding-decisions` | 读取建议与接受/驳回决定；采纳产生正式资料新版本 |
| `GET/POST /episodes/{id}/revisions`、`POST /episodes/{id}/restores` | 固定版本快照和恢复为新修订；expected branch/head |
| `POST /episodes/{id}/impact-previews` | 比较 base/target revision，返回分页受影响节点和解释 |
| `POST /series/{id}/run-plans`、`POST /series/{id}/runs` | 计划不派发；启动包含 plan_id/digest/expected revisions，202，输入变动409需刷新 |
| `GET /runs/{id}`、`GET /runs/{id}/nodes`、`GET /runs/{id}/events` | 状态/节点分页/有序增量事件游标；首版轮询，断线可补读，不依赖常驻浏览器 |
| `POST /runs/{id}/pauses`、`resumes`、`cancellations`（同前缀） | 显式命令，可重放；resume 不重发 unknown |
| `POST /run-nodes/{id}/attempts`、`review-decisions`、`recoveries`（同前缀） | 重新尝试/艺术决定/只读上游恢复；不同命令不得混用 |
| `POST /episodes/{id}/releases`、`GET /episodes/{id}/releases` | 创建封版并关联导出；传入全部源修订/审核决定，冲突时保留草稿 |
| `POST /series-import-previews`、`POST /series-imports`、`GET /series-imports/{id}` | 遗留多集项目拆分计划、显式确认执行和恢复状态 |

新增 OpenAPI schema 应在实施时落入仓库实际 OpenAPI 文件，规定 additionalProperties、字段上限、错误及空集合；本设计表不是已实现端点，也不替代机器可验证合同。既有 Project 生成/剪辑 API 继续使用，系列入口转换外部集号并保留快照。

## 7. 容量、运维与迁移

### 7.1 规模模型与目标

容量样本：200集 × 每集24镜 × 每镜图片/配音/可选视频3节点 = 14400个逻辑节点；加一次返工约28800尝试，另有剧本/分镜/导出和审片。每集60–120秒、1K–4K是目标；24镜只是负载样本，不是导演规则。当前10000调用、500任务、120素材和2GiB等产品额度在P0A移除并改实体存储/分页，不能拖到P5或删历史腾额度。

新表按 space/series/episode/node 查询并建必要索引；旧 core/media/usage/creative scope 热点逐步改实体仓储，避免每次派发重载全站历史。ModelCall 改按时间分区/游标分页和归档；dispatch/幂等回执单独保留摘要与状态，终态数据压缩后仍能拒绝重复，恢复中与封版引用不得清除。引用计数/保留期和 Blob 垃圾回收独立事务、可审计。

调度按text/image/video/tts/render区分资源池，遵守网关实际并发/429和租户公平轮转；资源满时排队，不按queued+running总数拒收。共享资源的公平权重不是租户调用总额。默认并发保守，扩容依据数据库锁等待、CPU/FFmpeg、带宽与供应商响应；本项目不自行维护上游余额或复制其额度账本。

需要指标：各阶段排队年龄/完成时间、活动租约、unknown数量/年龄、依赖阻塞、存储增长、scope锁等待、取消后完成、恢复重复派发检测、审核不可用。页面仅显示创作者有用的进度和处理方式；trace/request/attempt ID 放详情供排障。

容量与时延目标和基准环境定义见验收 AC-SCALE，不在没有测量时承诺实时生成。备份应包含新增表、Blob manifest、密钥保管方式与恢复演练，不只备份系列元数据。

### 7.2 分期迁移与回退

0. P0A先完成既有路径去配额、媒体/调用/回执实体化、流式Blob及DDL兼容、输出配置/分段渲染。旧作品保持原清晰度和时码，不因新默认值重新编码；新旧API/worker分批升级须明确禁止旧写入覆盖新实体。详细步骤见第11节。
1. P1 加表和可选来源字段；旧 templates 与旧 task 输入保持。新 private 权限有服务端测试，不能只增加 UI 开关。
2. P2 扩展配方语法与资产绑定；旧 syntax=plain。新增 library style/voice 类型使用显式版本，旧卡片不改类型。需同步 secure response/OpenAPI 合同。
3. P3 在停写迁移窗口或受控双写方案中添加稳定 ID 映射、series/season/episode；先 dry-run 对账，再幂等迁移；保留旧索引/名字快照。新集独立项目，旧项目不自动拆分。
4. P4 添加编排/修订/节点与 outbox 同事务，先内部开关、单节点和3集验证，再启用批次。所有 API/worker 版本兼容后才接新任务类型，旧 worker 不得认领未知 kind。
5. P5在P0A已去配额和流式实体化的基础上完成长期历史归档、容量/恢复演练。批量导出按集/片调度；单段执行和资源池有界，完整作品的镜头数、时长、文件大小不能沿用旧硬编码配额。

生产迁移必须备份、停止需停写的 API/worker、用新版本离线迁移器并核对运行角色新表 DML 权限；实际命令以实施后的 migrator 合同为准。本轮不执行。文件模式为个人小规模兼容；200集并发生产目标限定经容量验收的PG模式，不无证承诺文件全量快照能扩展。

回退应用前先停新 run；已派发保持可恢复。旧二进制不理解的新字段/表不让其写入，使用只读旧视图或前向修复；不能简单回滚镜像导致新数据被旧JSON写回覆盖。备份恢复要做同版本恢复/迁移演练；有外部已派发任务时单独对账，不用数据库回滚假装上游任务不存在。

## 8. 安全、质量与未决风险

| 风险 | 设计处理 / 剩余限制 |
| --- | --- |
| 私有模板经任务分享泄露 | 使用到共享项目明确提示快照可见范围；权限不靠前端；日志只记摘要，不能承诺已共享内容可撤回 |
| 模板/剧本中的指令注入 | 文本是创作数据，固定系统合同；工具访问/网络/凭据不交给变量，结构输出严格校验；AI建议不直接改正式事实 |
| 参考/声音授权不足 | 记录来源许可和授权状态，撤销后阻止新派发；既有成片的处理按明确产品政策决定，不伪造授权 |
| 200集角色漂移和剧情自相矛盾 | 版本固定、正式连续性账本、上下文证据和选片返修；真实效果必须样片评估，不能由单元测试保证 |
| 大规模任务重复、迟到、取消竞争 | dispatch边界、租约fencing、唯一attempt和原子入队；unknown不重发，迟到不覆盖采用 |
| 配置即用仍受技术条件限制 | 模型必须启用/真实能力匹配/凭据可用，审核需有效；展示具体原因，绝不改成预算阻止 |
| 高清长片超出现有实现 | 必须去掉分辨率/时长耦合，以分片拼接、音频/字幕全局时码和流式Blob实现所有1K–4K档60–120秒；不把旧15秒/128MiB门槛当成目标合同 |
| 长期历史撑满 | 按实体读写、归档与GC先于200集承诺；不能删除可恢复回执和正式引用来通过容量测试 |

未知真实模型延迟、人物一致性、供应商参考控制和声音许可均为剩余风险。Q1–Q7 的产品决策在对应阶段前解决；本轮设计不代替样片与生产演练。

## 9. 60–120秒与1K–4K的具体后期合同

### 9.1 输出标签与原生生成分离（FR-R1、FR-R2）

“1K/2K/3K”不是本项目可以假定的统一行业标准，采用明确产品映射并始终显示像素。建议首版成片档如下；实际能力响应返回最终width/height，不让客户端只传模糊K数：

| profile_id / 展示 | 16:9 | 9:16 | 1:1 | 时长目标 |
| --- | --- | --- | --- | --- |
| hd_1080 / 1K·1080p | 1920×1080 | 1080×1920 | 1080×1080 | 所有档60–120秒 |
| qhd_1440 / 2K·1440p | 2560×1440 | 1440×2560 | 1440×1440 | 同上 |
| qhd_1800 / 3K·1800p | 3200×1800 | 1800×3200 | 1800×1800 | 同上 |
| uhd_2160 / 4K·UHD | 3840×2160 | 2160×3840 | 2160×2160 | 同上 |

方形按对应短边输出并明示像素，不声称2160方图是3840方图。若用户需要DCI2K/4096宽等，作为额外明确profile验证，不把它混同上述档。旧360p/720p/1080p/2160p保持别名读取与原尺寸；新默认hd_1080，既有作品不被自动升级。现有图像画幅4:5继续兼容，项目9:16镜头图按原生能力裁切/填充预览；新增画幅同步目录/API/适配器，不把16:9图无提示拉伸为竖屏。

拆成三组版本化信息：

- `GenerationSpec`：模型/网关版本、原生尺寸或档、比例、每镜seconds、参考/声音等组合能力，`native_required`。只发上游实际支持的参数。
- `OutputProfile`：目标像素、比例、帧率（沿用24fps默认）、像素格式、编码/声音/字幕模式、`fit=contain|cover`、缩放策略；只作用于后期或明确支持的模型请求。
- `MediaProvenance`：实际探测的宽高/旋转/SAR/帧率/时长、模型声明尺寸、裁切/填充/普通升采样/增强方法和工具版本、原始素材/派生产物SHA、用户选择。

制作页先展示原生能力与目标输出的差异：“素材720p → 输出4K，普通放大，不增加原生细节”。默认允许明确展示的普通后期缩放，用户可选“只接受原生”；后者不满足时提示能力原因，不静默换模型。AI增强是另一个用户显式启动、需适配器/许可的节点；没有增强服务仍可普通渲染，不宣称增强或原生4K。

`GatewayModel`增加可选版本化`media_capabilities`：supported_sizes/profiles、ratios、duration组合、reference_roles、每请求输出数、codec，以及evidence_status=`declared/contract_tested/live_verified`和合同版本。`/models`目录名字不自动推断这些值，能力查询/保存也不触发收费探测；无精确信息时只用已核验适配器档，不发送自造4K参数。

### 9.2 导演与剪辑的时长设计

每集从节拍表确定冲突、情绪和信息释放，时间轴实际时长按镜头/转场计算。60–120秒是已确认产品目标；编辑时显示目标/实际差异，允许导演明确保留超目标草稿，不把“第121秒”当额度耗尽。

角色造型/服装/道具/场景风格固定版本；上一镜结束状态与下一镜开场状态进入生成上下文，剧中时间与发布集号分开。配音先读出对白再调镜头时长，保留呼吸、停顿和反应镜；自动转写/对齐按源音频块切分并合并全局时码，不保留ASR每小时10次或整集30秒的本地阻断。上游真实单次时长限制转为编排分片，不能篡改其能力声明。

低清动态分镜是独立preview artifact；采用后的正式渲染固定镜头/资产版本及全局时间轴。改变1K→4K只生成新的后期变体，不自动重写剧本/换候选；如果用户要求重新原生高分生成，明确列出受影响镜头，由人选择。

美术指导在低清预演中确认轮廓、留白、视线和色板，在目标画幅确认裁切安全区；标题、字幕、矢量叠层在最终目标像素重新排版/渲染，不把720p文字截图整张放大充当4K排版。角色/道具的细节缺失必须回到源候选处理，普通缩放不能替代美术定稿。

### 9.3 分段渲染算法（FR-R3）

1. 编译完整时间轴为不可变`RenderManifest`：目标profile、源asset/hash/trim、全局frame区间、音轨采样区间、字幕/叠层/关键帧和转场依赖。24fps下120秒为2880帧；使用有理数/整数帧与音频采样，不累计浮点误差。帧率变化/旋转在解码规范化阶段处理。
2. 计划器依据worker实测内存/CPU/临时空间把镜头或片段分成`RenderSegment`，单段目标时长是可调部署参数（例如10秒的起点），不是作品最大时长。镜头数、音轨、叠层超过单进程图容量则分组/分层合成，不拒绝整个项目。
3. 跨淡化边界保存左右handle，转场片明确拥有输出帧区间；其余段只输出不重叠core帧，防止重复帧/漏帧。所有段统一分辨率、pixel format、帧率、time base及色彩处理。跨段叠层/关键帧用全局时间求值，不能每段从零重启动画。
4. 图片/视频画面按段渲染到可恢复中间产物；checkpoint键包含manifest/hash、段区间、renderer/encoder版本和profile。重启仅重做缺失/校验失败段，旧租约无权提交。旧pairwise crossfade逐次重编码会随片数增长，不能直接无限套用；改为分段转场任务或经测量的有界合并树。
5. 音轨先按全局采样时码分组混音为连续stem，再最终复用或单次编码；不要每段各编码AAC再直接拼造成priming空洞。字幕在全局时轴生成；烧录时只渲染与段区间相交的cue，软字幕最终单次封装。转场不重复扣时，成片帧数与声音采样区间经验证。
6. 拼接前校验流/编解码参数，不能仅按扩展名stream copy；最终完整转封装MP4（交付可用faststart），分片中间格式与最终下载文件区分。最后probe与抽样解码检查像素/时长/轨道/字幕，完成后才原子发布artifact。取消/磁盘不足不能留下可见“成功”截断文件。

输出质量策略按profile设可配置编码质量/码率，不能为了塞进128MiB而暗降画质；保留源尺寸与放大标识。4K两分钟约8.3MP×2880帧的计算量是真实资源，执行时间可能远超播放时长；UI显示阶段与等待，不作实时生成承诺。

### 9.4 流式文件链路（FR-R4）

现有BlobStore以`[]byte`、128MiB常量与SQL CHECK为中心，必须一起改，不能只提高一个常量：

- 新流式端口拟定`PutStream(ctx,key,io.Reader,metadata)`、`OpenRange(ctx,key,start,length)`、`Stat/Delete`；端到端使用int64大小、增量SHA和有界缓冲；小文件旧Put包装器可保留，调用方不再ReadAll大视频。供应商下载同样直接流入临时/对象存储，审核/探测读取受控片段或文件流。
- 上传建立受授权`upload_session`、分块和最终complete；分块大小/请求解析预算保护连接，不限制整个对象。finish核对大小/SHA/MIME和归属后提交manifest，未完成块按保留期清理；恢复会话仍需当前权限。禁止任意URL上传/跨租户object key。
- 本地使用临时文件、fsync、同文件系统原子提交和磁盘高低水位；S3使用其实际分段上传能力及Range合同。处理前估计scratch并获取资源令牌，不扫描所有作品加到2GiB配额；真实ENOSPC保留checkpoint并进入waiting_storage或明确故障。
- `studio_blobs`把旧`size<=134217728`迁移为非负BIGINT和摘要/身份校验；Range校验以真实对象大小为界，不以MaxBlobBytes为界，处理溢出/越界/416。BlobSchema、health check、迁移工具、备份/恢复清单以及S3/本地实现同步更新。
- 删除FFmpeg `-fs 134217729`以及最终/中间128MiB拒绝，改每段scratch/执行预算和完整性校验；worker长任务使用续租/心跳与每段超时，不能把整片5分钟超时简单调大后视作完成。正常MP4封装与faststart需要的I/O空间仍应调度估计。

新增实体拟定：`RenderManifest/RenderSegment/RenderArtifact/UploadSession`；建议表`studio_render_manifests/segments/artifacts/upload_sessions`，全部包含space_id和project_id/episode_id，版本/unique checkpoint/状态可查询。source hash与output hash分别记录，完成产物、preview和原生素材不共用身份。

## 10. 无项目配额与运行安全的边界

### 10.1 运行时语义（FR-Q1–FR-Q3）

| 类型 | 用户可观察行为 | 保留或移除 |
| --- | --- | --- |
| 总素材/导出/集数/任务/历史/累计字节或按小时次数 | 跨旧阈值仍可保存、生成、续跑；无剩余额度UI | 删除本地业务拒绝，metadata分页、流式写/归档 |
| 网关余额/限流/单模型规格 | 显示upstream_quota/upstream_rate_limit/unsupported_media_spec及来源，可去网关处理 | 网关负责；不预估余额、不复制限额、不自动换模型 |
| CPU/解码内存/磁盘/连接忙 | 持久接受后queued，展示waiting_resource/waiting_storage及可取消操作；资源恢复继续 | 部署调度，按任务资源需求/公平权重，不按用户消耗总量 |
| 实际存储不可写，连队列都不能保存 | 503 storage_unavailable，未接受，客户端保留同键 | 真实失败，不能伪造202或把请求丢内存 |
| 权限/审核/引用/格式/SSRF/幂等 | 明确拒绝无权、危险或真实不支持的操作 | 必须保留，不是额度 |

429只在实际网关响应或明确服务级入口过载协议中出现，并标注origin；不会用429 library_limit/media_limit伪装人工项目额度。已持久接受任务本地并发满时继续排队；网关明确拒收的429可按其合同Retry-After退避，派发结果不确定仍保持unknown，不因去配额自动重发POST。

单个HTTP页、块缓冲、解码pixel预算和模型context有界；完整故事/素材通过分页/分块拆分，不能把一次请求上限转成项目总量上限。章节/候选数不写固定商业最大值；极大请求使用计划流与显式开始/取消，不自动无界创建任务或收费。

权限与成员撤销、人物授权、独立审核、幂等、dispatch持久回执和租约fencing照常生效。旧预算Reserve等方法只为历史维护，生成路径禁止引用；把非财务回执移出financial ledger scope后仍对旧unknown/历史ID做兼容去重。不要删除budget.json造成旧任务可被重新派发。

### 10.2 去配额不等于删除数据

先按实体读取/追加写，把creative、alignment、usage、media、library、review、social与恢复回执从整体快照热点剥离；归档按存储层策略只迁移冷数据，查询和幂等仍能到达，不能按10000条强删旧稿。旧receipt compact到带actor/operation/digest/terminal状态的可检索记录；unknown和release引用保留。

文件模式保持同样无业务配额语义，改分片append/manifest和检查点，不能无限放大64MiB快照/ReadAll；高并发目标仍由经过验证的PG部署承担。真实宿主磁盘、对象存储或网关物理能力可以耗尽，错误如实说明来源，不宣传无限基础设施。

## 11. 对照现有代码的改造顺序与API增量

P0A为当前路径优先增量，先于新模板/系列大改，详见计划T03–T07：

1. **仓储/额度**：`store.go`、`provider_creative_store.go`、`alignment_store.go`、`model_usage.go`、`media_store.go`、`production_store.go`、`library_binding.go`、`libraryrepo/*`、`image_resume.go`与social/digital_human路径，按L01–L08/L15去除累计/时间配额；新实体事务、分页和回执去重先齐备。`guardCreativeLocked`仅保留真实配置/版本/能力检查，删除20次小时判断。
2. **输出合同**：`modules/media/timeline.go`拆`OutputSpec`为尺寸/编码profile与资源计划，不再返回分辨率对应总时长上限；场/镜/轨/叠层长度改分批处理。`frontend/src/drama.ts`、`MediaStudio.tsx`、`mediaTimeline.ts`、中英文本和`api/openapi.json`同步删除999集/场镜条数、最长15秒/30镜/8轨等旧校验、按钮禁用和错误文案。`ProjectMedia`和发布页显示外部集号与真实像素。
3. **媒体执行/存储**：`media_render.go`、`render_segments.go`、`render_checkpoint.go`、`queued_export_worker.go`、`blob_store.go`、`blob_manifest.go`及backup/migration，落地9.3–9.4；修竖屏宽高/旋转/探测边界，按段fencing提交。不是全局搜索数字后一律删除。
4. **能力网关**：`gateway_models.go`、`gateway_runtime.go`、`gateway_provider.go`、`provider_*`与`creative_controls.go`，分别声明native组合/真实下载能力，去掉模型数/6000硬编码但保留实际上游/解析能力；旧未声明字段按已核验适配器默认读取，不伪造4K支持。
5. **迁移顺序**：备份→停旧写入/worker→加新表与Blob size约束迁移→流式复制/实体化及count/hash对账→升级API和worker→只读旧视图兼容→受控续跑；如需滚动升级先实现dual-read和单一writer，不让旧进程截断或覆盖新对象。大于128MiB产物存在后旧版不可安全恢复写入，回退用只读隔离或前向修复。

拟定新增接口均遵循会话/空间、幂等和版本CAS；现有tasks/creative/exports在无本地产品配额的情况下使用，未支持的原生参数明确失败：

| API增量（`/api/v1`前缀） | 合同 |
| --- | --- |
| `GET /media/output-profiles` | 返回精确像素/画幅/codec/fps、renderer版本与available/waiting/unsupported原因；profile可输出不代表某模型原生支持 |
| 现有运行时模型目录新增media_capabilities | 返回native组合/合同来源和版本，不泄露凭据、余额，不主动付费探测 |
| 现有timeline/export写入新增`output_profile`、`resize_policy`、`render_manifest_version` | 老output_resolution可省略，冲突别名400；有效写入返回202导出/计划ID，资源满只queued |
| `POST /projects/{id}/render-plans`、`GET /render-plans/{id}/segments` | 固定时间轴/素材/配置版本与全局时码；分段分页，不生成新模型素材 |
| `GET /media/exports/{id}/progress` | 按授权项目解析，返回持久completed/total阶段计数与waiting_reason/failed segment；不依赖浏览器在线 |
| `POST /projects/{id}/uploads`、`PUT /uploads/{id}/parts/{part}`、`POST /uploads/{id}/complete` | 分块、增量摘要、幂等提交和恢复；每请求有界但对象无128MiB商业上限，实际能力/资源错误明确 |
| 媒体/库/任务/调用/发布历史GET新增cursor/limit | 新客户端分页；旧全量响应进入明确兼容期，不能默默截断旧合同。后台新写路径不加载全部历史 |

profile和attempt快照覆盖实际参数与处理方法，不能把客户端提供的native=true当证据。所有新增端点/字段和旧别名在实施时写入`api/openapi.json`并同步安全响应合同；本轮不修改该接口文件。

## T32实施落点（2026-11-05）

旧多集转换已落实为每次显式复制一集，计划独立封存，源项目不改写；目标准备态隐藏到逐文件与引用对账结束。生产HTTP按持久版本推进单步，离开页面可暂停后回到同任务继续。具体协议、目录/Project事务、清单存储、迁移回退与剩余生命周期事项见[分集迁移实施记录](episode-migration-implementation.md)。本设计其余连续性、导演与批量制作仍为待实施内容。

## T33A实施校准（2026-11-05）

四类正式资料以统一`/series/{id}/story-records`版本合同落地，文件独立追加、PG为`studio_story_versions/heads`，正文不进入摘要列表。系列人物、主观认知和观众/角色知情时点显式区分。`context-previews`当前为只读固定来源的资料核对，不替代持久上下文与真实AI检查/建议采纳；具体落点和后续依赖见[故事资料基础记录](story-implementation.md)。

### T33C2A阶段落点

风格/声音意图卡、版本与参考数量门槛移除已按[资产意图记录](asset-intent-implementation.md)交付；声音卡目前只有表演意图，无真实声音身份、授权或克隆能力，风格卡也不自动注入模型。当前可撤销授权、实际图像/视频/声音派发适配仍按T33C2B/C继续实施，整体Spec未完成。

### T33C2B/C3实施校准

当前可撤销声明/CAS/原回执按[资产使用记录](asset-use-implementation.md)实现。旧素材按[来源追溯记录](asset-lineage-implementation.md)区分可信日志恢复与unverifiable历史；读取不改内容，复制不产生新许可，当前派发仍检查最新声明。新参考/媒体的服务端完整来源标记兼容缺省旧字段；旧无日志变换不靠当前海报或同名猜测。C2C2原生/参考/文字控制与C2D、编剧导演/编排及容量仍待实施。
