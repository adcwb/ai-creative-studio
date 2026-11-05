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
- [自定义图像提示词与长篇 AI 漫剧规格包](superpowers/specs/2026-11-04-creative-series/README.md)（2026-11-05修订5：每集1–2分钟、1K–4K、项目去配额；实施中，第一批结果与剩余工作见包内实施记录）

其他 `iteration-*`、`design/*` 和日期设计文档保留对应轮次的背景与验证记录；阅读时应核对基线、状态和后续专题文档。

当前实施证据：[纯文本自定义提示词](superpowers/specs/2026-11-04-creative-series/prompts-implementation.md)。完整Spec完成前不推送，变量及参考图项目制作闭环见[参考配方实施记录](superpowers/specs/2026-11-04-creative-series/references-implementation.md)，剩余基础能力及长剧仍按计划实施。
