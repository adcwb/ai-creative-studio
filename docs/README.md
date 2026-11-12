# 文档目录

[CI 故障修复与并行检查](superpowers/specs/2026-11-12-ci-performance/README.md)：最新失败日志、隔离并行方案、完整门禁与验证记录；本轮不推送，云端提速待实测。

本目录区分当前使用合同、历史迭代记录与未来设计。历史文档的费用、容量或供应商描述可能已过时，不能代替对应提交的代码和最新专题合同。

## 当前系统

- [项目入口与运行方式](../README.md)
- [架构与模块边界](architecture.md)
- [后端错误诊断、关联编号与安全日志](backend-error-diagnostics.md)
- [认证与组织](authentication.md)
- [网关模型与功能绑定](gateway-model-management.md)
- [运行时模型选择与成本管理兼容](runtime-model-selection.md)
- [执行治理与独立审核](execution-governance.md)
- [PostgreSQL、worker 与恢复](postgres-workers.md)（包含历史成本治理说明；成本限制已取消，以运行时模型选择文档为准）
- [真实服务验收边界](live-evaluation.md)

- [系统设置、空间与 RBAC 操作及迁移](superpowers/specs/2026-11-06-p0-administration/README.md)
- [创作入口、空间引导与9:16限定修复（本地审查）](superpowers/specs/2026-11-10-consumer-studio/README.md)

## 需求设计与实施规范

- [Spec Coding 文档约定与历史设计索引](superpowers/README.md)
- [自定义图像提示词与长篇 AI 漫剧规格包](superpowers/specs/2026-11-04-creative-series/README.md)（2026-11-06修订32：每集1–2分钟、1K–4K、项目去配额；实施中，第一批结果与剩余工作见包内实施记录）

其他 `iteration-*`、`design/*` 和日期设计文档保留对应轮次的背景与验证记录；阅读时应核对基线、状态和后续专题文档。

当前实施证据：[纯文本自定义提示词](superpowers/specs/2026-11-04-creative-series/prompts-implementation.md)。完整Spec完成前不推送，变量及参考图项目制作闭环见[参考配方实施记录](superpowers/specs/2026-11-04-creative-series/references-implementation.md)，剩余基础能力及长剧仍按计划实施。

文件资产库的64MiB累计快照瓶颈已移除，迁移和验证边界见[存储实施记录](superpowers/specs/2026-11-04-creative-series/storage-implementation.md)。整体规格尚未完成，暂不推送。

[可恢复上传实施记录](superpowers/specs/2026-11-04-creative-series/uploads-implementation.md)：会话/分块、跨刷新恢复、权限与迁移验收已交付；S3原生分段恢复后续已按[S3实施记录](superpowers/specs/2026-11-04-creative-series/s3-implementation.md)交付；渲染核心按[渲染记录](superpowers/specs/2026-11-04-creative-series/render-implementation.md)推进；长剧仍未完成。

[系列存储与独立项目基础](superpowers/specs/2026-11-04-creative-series/series-storage-implementation.md)：T31存储与原子保存子项、存储切换迁移已实现；该记录只覆盖当时的存储子阶段，系列前台见下方T31记录，旧多集拆分仍按计划推进，不提前推送。

[T31系列工作区实施记录](superpowers/specs/2026-11-04-creative-series/series-ui-implementation.md)：面向C端的季集目录、插入重排、单集真实网关输入与导出编号快照已交付；T32旧多集拆分及连续性、导演编排和完整生产容量仍在后续计划中，完整Spec完成前不推送。

[T32A稳定角色与场次关联](superpowers/specs/2026-11-04-creative-series/structure-implementation.md)：新版稿内身份、旧项目显式升级、分镜来源、幂等恢复与存储往返已实现；实际旧多集迁移、授权素材复制和迁移任务续跑尚未完成，不提前推送。

[T32旧多集分集复制](superpowers/specs/2026-11-04-creative-series/episode-migration-implementation.md)：显式预览、归属选择、目标独立素材、持久检查点、停止与崩溃对账已实现；原稿与旧成片保留。连续性与导演编排仍待实施，完整Spec完成前不推送。

[T33A故事资料与上下文基础](superpowers/specs/2026-11-04-creative-series/story-implementation.md)：正式圣经、认知/事件/伏笔的版本与C端编辑、知识边界、只读来源固定预览；真实AI检查/采纳、持久上下文检索与导演工作流仍未实现，不提前推送。

[T33C2B当前使用声明与既有媒体派发](superpowers/specs/2026-11-04-creative-series/asset-use-implementation.md)：撤销/CAS/原回执、前端恢复、可信媒体来源与派发前检查；完整风格/声音原生适配、旧衍生物全面追溯和导演编排仍未完成，不提前推送。

[T33C2C3旧素材来源追溯](superpowers/specs/2026-11-04-creative-series/asset-lineage-implementation.md)：按任务回执、库参考和迁移证明追溯，不明来源保留历史并明确用途边界；按需只读核对，不代表原生人物/风格/声音或完整制作编排已完成。

[T33C2D1配音消费来源固定](superpowers/specs/2026-11-04-creative-series/alignment-source-implementation.md)：独立识别/转写/强制对齐固定音频和使用修订、派发前复核，保持旧字幕与原回执；原生人物/风格/声音和完整制作编排仍在实施。

[T33C2C2人物、风格与原生音色实际输入](superpowers/specs/2026-11-04-creative-series/creative-inputs-implementation.md)：明确模式、固定版本/授权、真实协议及配音消费链；不承诺原生身份锁定或克隆，完整Go竞态、58项桌面手机网关浏览器、前端及HTTP合同通过；T33按当前支持矩阵完成，后续编剧导演编排仍未实现。

[T34A编剧候选与明确采纳](superpowers/specs/2026-11-04-creative-series/writer-candidates-implementation.md)：生成保留正式稿，比较/编辑后明确采纳，原请求恢复和当前版本CAS；季集节拍、固定故事上下文及后续导演编排仍待实施。

[T34B/C季集节拍与正式写作上下文](superpowers/specs/2026-11-04-creative-series/writer-context-implementation.md)：版本化计划、固定本集资料到真实text请求、来源变更检查与人工采纳；旧角色/对白数量门槛移除，64项桌面手机网关浏览器通过。T34完成，导演/制作编排和完整容量仍待实施。

[T41A导演意图与固定镜头来源](superpowers/specs/2026-11-04-creative-series/director-implementation.md)：明确文字/原生/参考/不支持边界，固定本镜与同集邻镜到真实请求，保留声音意图并按需读历史，比较不改剪辑，阶段完整Go/PG与66项网关浏览器通过；完整静帧预演和制作DAG仍待实施。

[T41B独立静帧预演](superpowers/specs/2026-11-04-creative-series/animatic-implementation.md)：真实静帧/临时声音、明确静音、独立来源与可恢复视频，不改正式剪辑或进入成片发布；T42–T45及长期容量仍未完成，整套Spec通过前不推送。

[制作修订快照/分支](superpowers/specs/2026-11-04-creative-series/revision-implementation.md)：T42当前子集；完整影响分析/恢复、批量和容量仍未完成。按[当前任务清单](superpowers/specs/2026-11-04-creative-series/README.md#current-task-list)优先完成单集真实制作—审片—合成，再推进批量及200集；完整Spec通过前不推送。

最新发布以[收紧后的本次范围](superpowers/specs/2026-11-04-creative-series/README.md#focused-release)为准：提示词、现有系列集管理、单集真实生成—人工审片—合成；画布、批量DAG和长期容量专项延期。聚焦验收后推送并核验CI，下方/历史记录中的旧完整Spec推送门槛不再适用于本次范围。
