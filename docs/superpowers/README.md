# Spec Coding 文档约定

核对日期：2026-11-04。仓库已有 `docs/superpowers/specs/YYYY-MM-DD-*-design.md`，未发现 `.specify`、Spec Kit constitution 或另行规定 Spec Coding 的 AGENTS/技能文件。以下是在现有目录内补充的规格编写约定，不宣称安装了 Spec Kit，也不追溯改变旧文档的状态。

## 目录与阅读顺序

小型需求可继续使用日期命名的单文件；涉及多个角色、领域和阶段的需求使用日期命名目录：

```text
docs/
  README.md                         文档总入口
  superpowers/
    README.md                       本约定与设计索引
    specs/YYYY-MM-DD-topic/
      README.md                     状态、基线、范围、决策入口
      research.md                   当前代码证据、差距与外部证据
      requirements.md               用户故事、需求、边界、假设
      design.md                     创作交互、领域、API、迁移与安全
      acceptance.md                 可执行验收合同与质量评估
    plans/YYYY-MM-DD-topic-plan.md   有依赖的实施任务和发布检查
```

## 编写规则

1. 先写创作者的问题、用户故事与可观察结果，再做设计与任务拆解。明确人作出的艺术决定和 AI 的辅助范围。
2. 每个规格包记录代码 SHA、日期、状态、未实现范围和待决问题。使用稳定 ID：`US-*`、`FR-*`、`NFR-*`、`AC-*`、`T-*`；需求—设计—验收—任务必须可追踪。
3. MUST 表示必须满足的合同，SHOULD 表示有理由时可调整的默认值。提案、假设、代码事实、实测结果分开表达；拟定 API 和表名不得描述为已存在。
4. 规格状态为 `Draft → Ready → Implementing → Verified`。Ready 记录已解决的关键产品决策；Verified 必须链接实现提交和真实检查结果。这里的状态是记录规则，不自动增加用户授权范围以外的审批流程。
5. 验收采用 Given / When / Then；覆盖成功、失败、权限、并发、恢复与旧数据。模型效果验收和确定性软件测试分开，合成上游不能证明真实视觉质量。
6. 任务使用未完成勾选框，注明前置依赖、目标文件、验收 ID 与退出条件。任务完成才更新状态，不能以写完文档代表实现完成。
7. 迁移先描述保留与回退边界；安全要求按现有认证、租户、凭据和审核合同延续。新规格不得恢复成本或用户额度门槛。项目条数/累计字节/小时次数与资源调度分开；资源保护须列出风险、排队/分段方案、实际能力和测试，不能只重命名为“安全上限”。
8. 相对链接应可解析，证据指向真实代码路径；行号以记录基线为准。旧规格只在明确写出的范围内被新规格替代。

## 设计索引

| 规格 | 状态与用途 |
| --- | --- |
| [自定义提示词与长篇漫剧](specs/2026-11-04-creative-series/README.md) | Implementing，2026-11-05修订9；提示词、上传/S3恢复及持久渲染核心已落地，完整回归见包内分阶段记录；系列长剧及容量仍未完成 |
| [多网关模型管理](specs/2026-10-30-multi-gateway-model-management-design.md) | 历史目标设计；当前合同见专题使用说明 |
| [分集制作与审片协作](specs/2026-10-29-production-phase-two.md) | 已交付轮次记录，包含历史费用机制 |
| [制作工作台](specs/2026-10-29-production-workspaces.md) | 历史设计与交付记录 |
| [可控创作总体设计](specs/2026-10-28-creative-production-design.md) | 历史长期提案，不能当成现有功能清单 |
| [可控创作首期](specs/2026-10-28-controlled-creation-phase1-design.md) | 历史首期设计，部分结构仅有领域规则 |
| [前端路由](specs/2026-10-28-frontend-routing-design.md) | 历史路由设计 |
