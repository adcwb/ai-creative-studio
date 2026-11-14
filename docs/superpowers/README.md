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
| [安全后端错误诊断](specs/2026-11-12-safe-error-diagnostics.md) | 兼容错误响应、请求与任务关联、脱敏上游元数据、panic 与 worker 恢复；准确验收及云端状态见规格 |
| [Token Plan 图像拒绝调查](specs/2026-11-12-bailian-image-diagnosis.md) | 本地请求与错误映射核验；生产根因须由失败记录确认，不把合成响应当成账号验证 |
| [CI 故障修复与并行检查](specs/2026-11-12-ci-performance/README.md) | 语言、懒加载布局与最新 Actions 故障；保持完整覆盖并拆分隔离 runner，实测与提速估算分开记录 |
| [自定义提示词与长篇漫剧](specs/2026-11-04-creative-series/README.md) | Implementing，2026-11-06修订32；提示词、恢复基础、系列迁移、故事/资产实际输入、编剧与T41导演预演已实现，准确证据见包内阶段记录；T42–T45、剩余P0A和长期容量仍未完成 |
| [多网关模型管理](specs/2026-10-30-multi-gateway-model-management-design.md) | 历史目标设计；当前合同见专题使用说明 |
| [分集制作与审片协作](specs/2026-10-29-production-phase-two.md) | 已交付轮次记录，包含历史费用机制 |
| [制作工作台](specs/2026-10-29-production-workspaces.md) | 历史设计与交付记录 |
| [可控创作总体设计](specs/2026-10-28-creative-production-design.md) | 历史长期提案，不能当成现有功能清单 |
| [可控创作首期](specs/2026-10-28-controlled-creation-phase1-design.md) | 历史首期设计，部分结构仅有领域规则 |
| [前端路由](specs/2026-10-28-frontend-routing-design.md) | 历史路由设计 |

- [Windows 原生后端重建规格与验收](specs/2026-11-13-windows-native-rebuild.md)

- [图像创作 CI 契约回归修复与验收](specs/2026-11-13-image-ci-contract-repair.md)

- [创作者返修、连续制作与团队交付](specs/2026-11-13-creator-workflow/README.md)

- [第6–10项补齐实施](specs/2026-11-13-creator-completion/README.md)（实施中；画布与数字人保留独立本地分支，先完成创作者功能）
- [可视化创作流程与数字人（本地阶段实现，完整规格未完成）](specs/2026-11-13-workflow-canvas-digital-human/README.md)
