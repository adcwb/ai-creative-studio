# 第八轮：执行治理接线

模型管理任务现在携带服务端绑定的租户、actor、配置 ID/版本、能力，以及用户确认的原币种和单次最大最小单位金额。worker 调用配置运行时之前必须经过 `ExecutionGovernor`；旧全局远端环境配置仍被启动检查拒绝，没有通过旧 worker 绕过治理的开关。

## 操作员配置

`EXECUTION_POLICY_FILE` 指向操作员控制的非秘密 JSON 文件；缺失时不启用付费执行，不能用默认价格或默认 allow。文件不会自动读取密钥。配置包含：

```json
{
  "version": 1,
  "moderation_command": "/absolute/path/to/review-adapter",
  "moderation_contract": "local-or-free-v1",
  "moderation_source": "REPLACE_WITH_LOCAL_IMPLEMENTATION_OR_VERIFIED_FREE_REVIEW_REFERENCE",
  "moderation_expires_at": "REPLACE_WITH_FUTURE_RFC3339",
  "billing_command": "/absolute/path/to/billing-adapter",
  "billing_contract": "local-or-free-v1",
  "billing_source": "REPLACE_WITH_LOCAL_OR_VERIFIED_FREE_BILLING_LOOKUP_REFERENCE",
  "billing_expires_at": "REPLACE_WITH_FUTURE_RFC3339",
  "prices": [{
    "provider": "openai-text",
    "model": "EXPLICIT_REVIEWED_MODEL",
    "capability": "text",
    "currency": "USD",
    "request_contract": "text-64k-6000-v1",
    "input_minor_per_million": "REPLACE_WITH_VERIFIED_DECIMAL",
    "output_minor_per_million": "REPLACE_WITH_VERIFIED_DECIMAL",
    "maximum_input_bytes": 32000,
    "source": "REPLACE_WITH_OFFICIAL_PRICE_AND_REVIEW_REFERENCE",
    "expires_at": "REPLACE_WITH_FUTURE_RFC3339"
  }]
}
```

此示例刻意不可直接启用，没有真实价格、供应商授权或凭据。`text-64k-6000-v1` 仅支持 OpenAI/Anthropic 纯文本协议：请求完整编码最多 64KiB，最大输出/推理预算 6000 tokens。预算按 65536 输入 tokens 与完整 6000 输出 tokens 的保守上限、明确原币种十进制最小单位每百万 token 费率向上取整。价格必须匹配具体模型，不能把美元价格当人民币。模型生命周期与协议支持仍单独验证。

其他能力没有通用报价。必须提供 `request_sha256`（`ExecutionRequestDigest` 对具体文本、参数、媒体内容摘要的摘要）、`maximum_minor`、原币种、来源和过期时间；界面不把此类逐请求规则当成任意创作的通用报价。报价是受控请求的费用预留依据，不是供应商账单担保；实际超额仍如实记录并阻止继续超预算。

## 审核与费用证据命令

审核在模型发送预留之前执行，因此当前只支持操作员明确核验的纯本地或免费审核。必须提供 `moderation_contract: "local-or-free-v1"`、核验依据 `moderation_source` 和未来 `moderation_expires_at`；缺失、未知收费或过期在启动及每次审核前拒绝，根本不运行命令。收费审核尚未有独立计量／预留链，不能用模型生成预算冒充已覆盖审核费用。操作员声明必须真实：此契约不是对命令网络行为的沙箱证明。

命令必须是绝对路径可执行普通文件，不经 shell 拼接；10 秒超时、16KiB 标准输出限制，标准错误丢弃、不继承宿主环境（仅固定 PATH），无密钥日志。stdin 是 JSON；审核收到 `ExecutionRequest`（Go 字段名，包括 Text、HasMedia、Media 的 base64 Data/MIME、Stage；输出审核 Stage 为 output），最多 64MiB。收到媒体标志但没有媒体内容会拒绝。命令需要接入实际审核策略，不能以固定 allow 作为生产审核。

审核输出使用 `ModerationDecision`：`provider`、`model`、`status`、`categories`、`checked_at`；`checked_at` 必须在过去 5 分钟内且最多允许未来 30 秒时钟偏差；仅完整 allow 通过，未知字段、尾随 JSON、失败、review、unavailable 都拒绝。脚本/分镜/分析输出与媒体生成产物在发布前复审；输出审核失败不会释放已发送请求的费用。

配置费用命令还必须提供 `billing_contract: "local-or-free-v1"`、`billing_source`、`billing_expires_at`，仅允许核验为本地或免费的账单读取。缺失或过期不会运行费用命令，既存预留继续 unknown；收费账单查询未接独立预留，不能启用。完全不配置费用命令仍允许生成，但不会凭空结算。

费用命令仅收到 ID/Tenant/Provider/Model/Capability，不包含提示词或媒体。输出为 `State`（settled/released/unknown）、`ActualMinor`、`Reference`。仅可信账单证据可以 settled/released；HTTP 成功不是金额证明，HTTP 错误不是免费证明。未配置、超时、无证据或未知状态均保留全部费用预留。

## 原子性和恢复边界

`ClaimDispatch` 在账本同一锁/事务中写入 unknown 和 dispatch_started 后才调用供应商；重复 ID 不重新发送。PostgreSQL 的任务接收预留与队列事务接线以数据库实现测试为准；执行时不会把已预留的较大用户上限缩小成较小估价。配置/审核/报价失败仅原子释放尚未 dispatch 的 reserved 条目。运行时可证明一次网络尝试都未发生时才释放；已尝试的超时/崩溃保留 unknown。单独取消、重新启动不能推断供应商未收费。

本轮测试只使用合成价格、合成凭据、假 transport 和本地 fixture 命令。没有配置真实审核服务、价格或账单适配器，没有执行真实模型。

已知上游任务的恢复查询另走 `ExecuteRecovery`，必须已有同租户/模型/币种的 unknown 费用条目，且对应新鲜报价规则显式 `recovery_queries_free: true`。这只表示操作员核验该供应商查询/下载不收费；没有此配置拒绝恢复。运行时只允许精确绑定已持久上游 ID 的官方视频 GET 查询，以及即梦原生 adapter 的固定签名 POST 查询（固定 endpoint/action/version/req_key/task_id，拒绝额外字段）；不能再发创建或参考图上传。恢复前错误或成功查询均不释放旧 unknown，配置版本/权限撤销仍阻止恢复。即梦 POST 查询例外不是任意 POST 授权，细节见 [模型管理](model-management.md)。
