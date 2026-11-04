# 模型执行与内容审核

模型执行已移除成本管理。API 和 worker 不再要求报价、费用确认、价格依据、费用预留或预算，历史额度和未知费用不会阻止新调用；也不运行账单命令推断费用。供应商可能正常收费，实际费用以供应商账单为准。

## 仍需满足的执行条件

- 已认证的组织角色具备执行权限，模型已启用且凭据可解密。
- 当前配置版本、实际能力及功能绑定授权匹配；不能借文本模型生成图片。
- 有效的内容审核程序审核实际输入与输出模态，拒绝、过期或故障时停止提交或发布。
- 持久化发送凭证防止同一任务重复调用；并发、载荷、超时和存储边界仍生效。

## 审核配置

可配置 `GATEWAY_MODERATION_COMMAND`、`GATEWAY_MODERATION_SOURCE` 与 `GATEWAY_MODERATION_EXPIRES_AT`，或使用 `EXECUTION_POLICY_FILE`：

```json
{
  "version": 1,
  "moderation_command": "/absolute/path/to/review-adapter",
  "moderation_contract": "local-or-free-v1",
  "moderation_source": "REPLACE_WITH_REVIEWED_IMPLEMENTATION_REFERENCE",
  "moderation_expires_at": "REPLACE_WITH_FUTURE_RFC3339"
}
```

该示例不能原样启用。审核命令须由部署方安装并声明为本地或免费执行，必须覆盖实际模态；此独立策略没有在成本管理改造中关闭，也没有默认“始终允许”审核器。命令以 JSON 读取 `ExecutionRequest`，返回带 provider、model、status、checked_at 的审核结论，输出阶段 `Stage` 为 `output`。只有新鲜的明确 allow 才通过。命令不继承部署密钥环境，受执行超时和输出大小限制。

策略不再需要 `prices` 或 billing 设置。旧价格／账单字段可兼容读取，但过期不会阻止生成。音乐旧适配器的供应商账户资格、参考素材传输授权等独立许可仍保持校验。

## 持久化与兼容

新的执行凭证包含任务、租户、供应商、模型和发送时间，不包含金额或额度。文件模式将凭证与旧 `budget.json` 的历史数据分别保存在独立字段；PostgreSQL 使用 `studio_model_dispatches` 表。原 schema 迁移、备份、恢复和导出流程覆盖该集合。已有数据库须先完成 schema 迁移后启动新版本。

原预算、费用流水和变更审计不删除、不重算、不用于准入；界面只读展示历史费用。旧预算 API 和离线维护命令仅供兼容保留，不能重新给新调用启用预算限制。新调用不制造“零费用”或已结算记录，使用统计保留次数、状态、耗时和上游报告的 token；未报告即未知。

已发出或结果未知的历史任务不得因升级自动重试。恢复只能查询已有上游任务的受限 GET 路径，不创建新的生成请求，也不需要查询费用报价。取消、超时和未知结果都不能说明供应商未收费。

独立的命令行付费评测工具保留其自身显式授权和本地记录要求；这不参与创作页面和服务端模型准入。
