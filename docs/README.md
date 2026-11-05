# 文档目录

本目录区分当前使用合同、历史迭代记录与未来设计。历史文档的费用、容量或供应商描述可能已过时，不能代替对应提交的代码和最新专题合同。

## 当前系统

- [项目入口与运行方式](../README.md)
- [架构与模块边界](architecture.md)
- [认证与组织](authentication.md)
- [网关模型与功能绑定](gateway-model-management.md)
- [运行时模型选择与成本管理兼容](runtime-model-selection.md)
- [执行治理与独立审核](execution-governance.md)
- [PostgreSQL、worker 与恢复](postgres-workers.md)（包含历史成本治理说明；成本限制已取消，以运行时模型选择文档为准）
- [真实服务验收边界](live-evaluation.md)

## 需求设计与实施规范

- [Spec Coding 文档约定与历史设计索引](superpowers/README.md)
- [自定义图像提示词与长篇 AI 漫剧规格包](superpowers/specs/2026-11-04-creative-series/README.md)（2026-11-05修订23：每集1–2分钟、1K–4K、项目去配额；实施中，第一批结果与剩余工作见包内实施记录）

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
