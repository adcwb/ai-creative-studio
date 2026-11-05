# 代码调研与证据

基线、状态和范围见[规格入口](README.md)。调研基线为2026-11-05 fetch确认的`9e687c584632fdc98628c42592ff3df2ff0304cb`。下表描述该基线的原有限制，后续实施已改变其中一部分；当前改动/验证见[实施记录](implementation.md)。下表是静态代码核对，不代表真实供应商、高清渲染或200集负载实测。行号属于该SHA。

## 当前能力与差距

| ID | 已确认的代码事实 | 复用与差距 |
| --- | --- | --- |
| E01 | [catalog.go](../../../../backend/internal/application/catalog.go) L3–33：三个静态 Template，携带 Input；[http.go](../../../../backend/internal/application/http.go) L28：GET templates | 可保留内置模板合同；没有用户模板 CRUD、变量、搜索或权限模型。肖像描述仍有“仅提供模拟画面”的历史文案，后续应修正文案，不能推断当前生成仍用 mock |
| E02 | [TemplateGallery.tsx](../../../../frontend/src/components/TemplateGallery.tsx) L5–12、[Composer.tsx](../../../../frontend/src/components/Composer.tsx) L25–30 | 现有选用入口与可编辑输入可复用；当前自定义内容不能保存成模板。内置文案走翻译函数，用户原文不能当翻译 key |
| E03 | [generation/input.go](../../../../backend/internal/modules/generation/input.go) L19–20：解析后的提示词 3–1000 Unicode 字符；比例/风格也经校验 | 模板展开必须服从当前输入合同，不能仅用浏览器 textarea maxlength 或截断文本 |
| E04 | [library/model.go](../../../../backend/internal/modules/library/model.go) L54–87、L135–152：稳定资产 ID、不可变版本、摘要、明确版本绑定；种类只有 character/product/scene | 角色/场景版本可复用；没有 prompt/style/voice 类型，不能把 Description 当模板数据库。资产参考最多 12 张，不等于某模型一次能接收 12 张 |
| E05 | [library_http.go](../../../../backend/internal/application/library_http.go) L59–80、L91–175；[libraryrepo/store.go](../../../../backend/internal/persistence/libraryrepo/store.go) L211–253 | 现有空间权限、幂等写入、版本冲突、游标、归档可借鉴。列表按 kind/status 过滤，无全文搜索、标签和个人私有层。PG 列表已按空间查询，不应全部重写 |
| E06 | [library_binding.go](../../../../backend/internal/application/library_binding.go) L14–19、L44–65 | 项目绑定明确资产版本，归属和用途校验可复用；新增风格/声音需新类型、合同与校验，不只是加下拉选项 |
| E07 | [projects/script.go](../../../../backend/internal/modules/projects/script.go) L12–33、L73–112 | 已支持最多 30 角色、1–120 场景，场景集号 0–999；角色以名字引用，场景无稳定 ID。没有独立系列、季、Episode 实体、角色弧线或正式连续性账本 |
| E08 | [projects/storyboard.go](../../../../backend/internal/modules/projects/storyboard.go) L11–28、L69–104 | 镜头稳定 ID、目的/表演/连续性说明已有；1–120 镜，场景仍是 scene_index，景别与运镜枚举有限。导演控制需要逐步扩展，不能声称已有完整摄影语言系统 |
| E09 | [media_episodes.go](../../../../backend/internal/application/media_episodes.go) L3–11；[media_store.go](../../../../backend/internal/application/media_store.go) L249–274 | 第一集旧字段、其余 episode_timelines；最多 119 个额外时间轴，共享媒体版本。集号可到 999 不代表能存 999 集；已有 120 集限制不满足 200 集 |
| E10 | [media_store.go](../../../../backend/internal/application/media_store.go) L209；[media_http.go](../../../../backend/internal/application/media_http.go) L20；[media/timeline.go](../../../../backend/internal/modules/media/timeline.go) L42、L130–151 | 每项目120素材/30导出、时间轴最多30镜；360p≤180s、720p≤120s、1080p≤60s、2160p≤15s。均为当前阻断点，新设计须消除产品配额和分辨率/时长耦合，资源保护改为分段与排队 |
| E11 | [ProductionBoard.tsx](../../../../frontend/src/features/projects/ProductionBoard.tsx)；[production_store.go](../../../../backend/internal/application/production_store.go) L25–79；[media/production.go](../../../../backend/internal/modules/media/production.go) | 候选比较、采用、素材时间点批注、负责人、优先级、解决/重开已有；采用是追加记录，不直接替换时间轴。不等于有整集终审、发布封版或跨集变更影响工作流 |
| E12 | [provider_creative_store.go](../../../../backend/internal/application/provider_creative_store.go) L219–252、L495、L560、L800；[task_acceptance.go](../../../../backend/internal/application/task_acceptance.go) L113–126 | 创作记录500/回执1000/活跃4。L495仅在无selection时限制20次/小时，但剧本/分镜的guardCreativeLocked另有全局20次/小时，显式selection也会进入；旧版“显式选择已无所有小时限制”的概括不准确，修订2更正。视觉比较不保证身份锁定 |
| E13 | [jobqueue/queue.go](../../../../backend/internal/platform/jobqueue/queue.go) L83–114、L138–155、L213–224；[worker/main.go](../../../../backend/cmd/worker/main.go) L129–137 | PG 持久租约、fencing、SKIP LOCKED、dispatch 边界和查询恢复可复用。全局活跃 8、每租户 4，种类上限 2 或 4；每 worker 并发默认 2、可配 1–16。增加 worker 不自动提高所有上限；尚无系列 DAG/人工 gate/公平调度仪表 |
| E14 | [postgres.go](../../../../backend/internal/application/postgres.go) L176–208、L372–409 | 多个 scope 的兼容状态读取锁定 scope 行并加载对应 collection 的全部记录；增量写不等于增量读。长篇新领域应按空间/集读写，逐步剥离热点，不能仅加索引宣称已解决 |
| E15 | [model_usage.go](../../../../backend/internal/application/model_usage.go) L97–123；[gateway_transport.go](../../../../backend/internal/application/gateway_transport.go) L67–75 | 当前调用历史 10000 条容量，保存失败会在网络前返回不可用。它是记录存储限制，不是被移除的预算限制；长期生产须分区/归档且保留执行回执，避免做到一半不可调用 |
| E16 | [history.go](../../../../backend/internal/application/history.go) L9–20；[revisions/model.go](../../../../backend/internal/modules/revisions/model.go) L18–55、L94–112 | 应用已有剧本/分镜历史恢复。revisions 模块已有 Manifest、Branch、Edge 纯规则，但在此基线未检出应用/仓储集成引用；不能说完整分支修订工作台已落地 |
| E17 | [ImagePage.tsx](../../../../frontend/src/pages/ImagePage.tsx)；[project_http.go](../../../../backend/internal/application/project_http.go) L131；[storyboard_http.go](../../../../backend/internal/application/storyboard_http.go)；[model_runtime.go](../../../../backend/internal/application/model_runtime.go) | 图像、剧本、分镜走真实网关功能绑定；文本/image/video/tts/vision 等按能力区分。文字模型不能作为图片或视频模型的替代；缺少模型、配置被撤销或上游失败不能回退演示结果 |
| E18 | [runtime-model-selection.md](../../../runtime-model-selection.md)、[execution_dispatch.go](../../../../backend/internal/application/execution_dispatch.go)、[execution_policy.go](../../../../backend/internal/application/execution_policy.go) | 成本管理已移除，保留非财务 dispatch receipt、实际权限/能力与独立内容审核。长篇计划不能恢复报价/预算步骤；缺失或过期的独立审核仍会阻止执行，需明确配置说明 |
| E19 | [README 的社交发布边界](../../../../README.md)、[modules/publishing/model.go](../../../../backend/internal/modules/publishing/model.go) | 已有真实导出与平台授权/发布状态边界；导出、草稿提交、待审核、未知结果均不能标成对外已发布。长篇交付必须延续此区别 |

## 架构取舍

提示词采用单独的 `prompts` 领域，与现有 library 共享“版本固定的资产引用”，复用其幂等、授权和仓储模式，避免把结构不相容的模板塞进 character/product/scene 卡片。首期只做纯文本及参数；参考配方后续接到项目制作入口，不把首页无参考的请求谎称为支持编辑图片。

长篇采用模块化单体中的 `series` 与 `productionruns`，继续使用现有 PG、worker 和 BlobStore。每集独立关联 Project，让剧本/分镜版本、媒体限额和修改冲突按集隔离；系列信息用分页元数据汇总。相比把 200 集继续堆进一个 MediaState，可减少共享版本冲突，也更易复用现有编辑器。代价是旧项目拆分、跨项目资产复制授权、系列回溯和原子协调需要明确设计。

## 外部原始资料核对

仅用于验证机制边界，不构成供应商选型或真实效果评测：

- [PostgreSQL 18 SELECT / locking clause](https://www.postgresql.org/docs/current/sql-select.html)（2026-11-04）：SKIP LOCKED 适合多个消费者访问队列表，不能替代普通业务读取的一致性。当前队列可复用，版本和依赖发布仍需事务/CAS。
- [OpenAI 图像生成限制](https://developers.openai.com/api/docs/guides/image-generation#limitations)（2026-11-04）：重复角色与品牌元素仍可能失去一致性。因此“保存提示词和参考”不能承诺跨 200 集自动锁脸，必须保留选片与人工审片。

本次没有调用真实模型，没有测量现有数据库的 200 集容量；下面文档中的规模、SLO 和质量阈值都是拟定验收目标。

## 修订2：限制分类与逐项处置

Q类＝必须移除的产品/账户/累计配额；C类＝真实模型、平台、输入或媒体合同；S类＝必须保留但改为资源调度/流式执行的保护。混合条件必须拆开，不能整体删除安全校验或整体保留配额。下列均为静态代码事实，目标处置尚未实现。

| ID | 代码与当前条件 | 分类与具体改造 |
| --- | --- | --- |
| L01 | `media_store.go:209,266,301–328`：120素材、119额外时间轴、扫描整个媒体目录累计2GiB并为每活跃导出计256MiB | Q：删除总条数/累计字节拒绝及固定256MiB商业预留；按实体存储/分页。S：按真实剩余磁盘、临时空间与作业估算调度；没有空间等待或明确存储故障，不能按作品用量拒绝 |
| L02 | `media_render.go:192`：queued/running≥2、Exports≥30、overQuota同时返回errMediaLimit | 拆分：30次历史为Q删除；2个活动总量包含queued，不能当纯worker调度保留。入队持久化后202，实际running受部署资源池限制，历史分页 |
| L03 | `provider_creative_store.go:238–252,495,560,1091`；`store.go:458` | Q：去掉全局20次/小时、selection缺失路径20次/小时、500job/1000receipt；S：active4改为待调度，不拒绝可持久化新请求。已存在同一输入修订的不可兼容并行写用CAS/分支处理，不能新增每用户活跃配额 |
| L04 | [alignment_store.go](../../../../backend/internal/application/alignment_store.go) L262、L274–290、L356 | Q：500任务/1000回执、20次/小时、ASR/转写10次/小时均移除；S：active2和同项目active判断改节点排队/CAS；C：当前transcription≤30秒音频应分段转写与全局时码合并，不是假装整集已支持 |
| L05 | `model_usage.go:106–107`、`gateway_transport.go:67–75`；[image_resume.go](../../../../backend/internal/application/image_resume.go) L53 | Q：调用历史10000条、恢复回执1000条会真实阻断，改实体插入/索引/归档和紧凑终态回执。保留派发前持久化，不能通过忽略审计写失败解决 |
| L06 | `libraryrepo/store.go:417–421`、`libraryrepo/postgres.go:76–85`；`library_binding.go:149–156` | Q：库总参考128MiB、1000资产/10000版本/20000回执、项目60绑定/120素材、累计2GiB全部移除；版本/引用分页，PG与文件模式同语义。S：旧文件64MiB全量JSON不无限ReadAll，迁移为分片/日志和快照 |
| L07 | `production_store.go:56,64`；[digital_human.go](../../../../backend/internal/application/digital_human.go) L61 | Q：采用/审片各2000、数字人/回执1000，改独立追加表/分页；人像/声音授权与幂等继续校验 |
| L08 | `projects/script.go:73,92`、`projects/storyboard.go:70,81`；`media/timeline.go:30,39,42,177` | Q：角色30/场景120/镜头120/额外时间轴119、编号999、时间轴30镜/8音轨/4叠层作为完整项目计数不再拒绝。C/S：单次模型输出、HTTP页、滤镜图有界，分页/场级生成/分组渲染，保留ID、时间区间、有限数值与引用有效性 |
| L09 | `media/timeline.go:102–104,128–151`；[MediaStudio.tsx](../../../../frontend/src/components/MediaStudio.tsx) L154、L183 | 旧分辨率/时长耦合是实现保护，不是上游限额；前后端一起改。1080p和4K均须完成120秒，新增1440p/1800p；渲染器按片分配资源，不能保留“4K15秒”文案或按钮禁用 |
| L10 | `media_http.go:138`：上传ReadAll32MiB；`media_store.go:209`；`media_render.go:470`、[media_composite.go](../../../../backend/internal/application/media_composite.go) L12–24：最终/中间128MiB、FFmpeg -fs=134217729 | S实现边界须重构：分块/流式上传、流式产物、完整性校验，删除截断-fs和以文件大小拒绝完整作品；真实磁盘不足停在检查点。24Mbps×120秒的视频量级约360MB（未计音频/容器，估算非实际恒定码率），128MiB不能保障4K两分钟 |
| L11 | [blob_store.go](../../../../backend/internal/application/blob_store.go) L26、L53、L100、L265、L312–360；[blob_manifest.go](../../../../backend/internal/application/blob_manifest.go) L310–317 | MaxBlobBytes=128MiB贯穿Put/Read/Range及PG CHECK(size≤134217728)，不是只改渲染参数即可。改流式接口与int64对象大小/Range、DDL约束、校验/备份/导入/导出同升级；小块缓冲仍有界 |
| L12 | `media_render.go:78,95`：输入最长180秒、width≤3840且height≤2160；`media_render.go:220`、`queued_export_worker.go:70`：5分钟超时 | C/S：源媒体长度不能由成片目标倒推，按需探测/截取；高宽独立上限会拒绝2160×3840竖屏4K，需按显示旋转、实际像素和解码能力验证。超时改每段预算/心跳/检查点，不把整片时长当额度 |
| L13 | [provider.go](../../../../backend/internal/application/provider.go) L128–136；`gateway_provider.go:70,102,126–127`；`provider_google_media.go:285`；`provider_minimax_media.go:138–146` | C：图像尺寸固定1024方/1024×1280/1536×864；视频来自配置VideoSize/VideoSeconds或适配器固定档。当前只有粗粒度能力，没有统一原生1K–4K协商；32MiB视频下载也须流式。不能把输出4K字段直接发送给任意模型 |
| L14 | `jobqueue/queue.go:114,220–224`、`worker/main.go:129–137` | S：全局8/租户4/类型2或4、worker默认2是执行并发，不是累计额度；改可观测公平资源调度，满槽任务持续排队，不用其拒绝新项目/任务。保留租约fencing、防重复派发和实际供应商并发合同 |
| L15 | [social.go](../../../../backend/internal/application/social.go) L124；[social_douyin_runtime.go](../../../../backend/internal/application/social_douyin_runtime.go) L210、L310；[social_photo_normalize.go](../../../../backend/internal/application/social_photo_normalize.go) L103 | Q：项目发布历史100条/照片入库120和累计2GiB删除，不能在交付末端又用历史额度阻断。C：TikTok/抖音真实媒资规格、授权、发帖意图去重和平台限流保留；本轮不声称4K输出必然可直接发布到所有平台 |
| L16 | [gateway_models.go](../../../../backend/internal/application/gateway_models.go) L193、L275、L349；[gateway_sync.go](../../../../backend/internal/application/gateway_sync.go) L66；`gateway_transport.go:282`；[model_config.go](../../../../backend/internal/application/model_config.go) L288 | 相邻配额旁路：50网关/200模型/旧100配置/目录最多200会影响“配置好就用”，计划移除人为条数门槛并分页/流式同步；6000输出token不是余额但当前硬编码也需改为实际模型能力，结构输出仍分场/有界解析。未知能力不能据目录名字推断 |

上述路径均相对`backend/internal/application`，明确写`projects/*`、`media/*`的路径相对`backend/internal/modules`，`libraryrepo/*`相对`backend/internal/persistence`；其余完整路径由表内链接与E表定位。

前端镜像限制也需一起改：[drama.ts](../../../../frontend/src/drama.ts) L32–44、L68–75校验角色/场景/镜头与999集编号；MediaStudio在素材添加、8音轨按钮、outputInvalid和保存/导出disabled上提前拒绝，`mediaTimeline.ts:42,61`保留4叠层/8音轨判断。仅删除服务端if不会满足用户流程；T04/T06及AC-Q03覆盖前后端。

范围边界：登录防爆破、会话过期、SSRF/网络允许范围、加密密钥、成员授权与内容安全不因“去额度”移除。组织/成员/社交连接数量（`auth_http.go:323,404`、`social_runtime.go:329`）属于相邻平台管理限制，T03默认移除纯账户计数配额并分页，不改变角色/授权制度；不能误说这些计数都属于安全规则。每页条数、单块字节、每个解析器内存预算是S，不能变成总作品上限。符号检查、有限时码、源时长边界与真实供应商能力属于C。

## 修订2：旧预算逻辑的真实调用关系

静态核对生产Go调用点（排除测试）：

- [governance.go](../../../../backend/internal/application/governance.go) L208–263仍有Reserve/ClaimDispatch/reserve及“budget exhausted or missing”判断。Reserve仅见[cmd/budget/main.go](../../../../backend/cmd/budget/main.go) L42离线历史维护调用；ClaimDispatch未见生产调用者。不是“删除了所有预算代码”，也不是“正常创作仍要预留预算”。
- [postgres_transaction.go](../../../../backend/internal/application/postgres_transaction.go) L80的ReservePostgresTx未见生产调用点，为遗留辅助方法；不能拿其旧注释作为新DAG事务合同。
- 正常生成执行入口[execution_governance.go](../../../../backend/internal/application/execution_governance.go) L58–80调用[execution_dispatch.go](../../../../backend/internal/application/execution_dispatch.go) L17的claimExecution；后者只持久化去重回执并兼容升级前reserved/unknown状态，不读取Limits或余额。恢复用hasExecution，并重新校验授权。
- 旧budget历史读取、装载/损坏检查和离线对账属于数据兼容，不建议直接删文件或表；将非财务receipt拆为独立仓储后解除启动对财务历史数量/全量快照的运行耦合。遗留维护API/命令显式标为历史用途，绝不再次接入生成或编排。

## 修订2：后期依据与核验边界

[FFmpeg concat合同](https://ffmpeg.org/ffmpeg-formats.html#concat)要求拼接输入流/编解码器/time base等一致；时间戳和duration不准确会产生接缝问题。[MP4分片合同](https://ffmpeg.org/ffmpeg-formats.html#Fragmentation)说明fragmentation可减少长文件封装的内存需求。2026-11-05已核对；分片格式不是自动证明所有播放器可播放，最终交付需完整转封装和解码验收。

FFmpeg滤镜页直连获取曾超时，官方检索可定位xfade一致性要求；外部资料本身不能证明代码或4K实测通过；本批真实编码结果见实施记录。精确分段/字幕/音轨算法与测试目标见设计第9节和AC-R系列。
