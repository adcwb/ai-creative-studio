# 后端错误诊断

本合同适用于 API server、worker 和当前内置网关适配器。它补充现有错误码、审计和 trace，不改变权限、租户隔离、幂等、结果未知时禁止自动重发等规则。无需新增环境变量或数据库迁移。

## 从一次失败定位

1. 在浏览器开发者工具的失败请求中保留响应头 `X-Request-ID`、`X-Correlation-ID`，以及 JSON `error` 的诊断字段。不要复制 Cookie、Authorization、请求正文或完整 HAR。
2. 使用 `request_id` 查找 API 的 JSON 控制台日志。异步创建成功后再失败的任务，应使用任务查询结果的 `correlation_id` 查找 API 和 worker 两侧日志，而不是只看创建请求的 202 状态。
3. 网关调用记录的 `correlation_id` 对应上述调用链；`request_id` 保持原来的上游请求编号语义，与 HTTP 响应中的服务请求编号不同。记录还包含最后一次响应的 `http_status`、`upstream_error_code`、调用次数和执行状态。
4. 若有上游元数据，核对网关厂商、地址、模型和所支持参数，并到供应商控制台按其请求编号查找。HTTP 400/403 或 `provider_rejected` 本身不能证明具体原因。
5. `execution_interrupted`、网络超时或派发后异常可能意味着上游已经执行。先核对任务及供应商记录，再决定显式重试；诊断代码不会自动重发。

示例（字段为合成值）：

```json
{
  "error": {
    "code": "provider_rejected",
    "message": "上游拒绝请求",
    "request_id": "1234567890abcdef1234567890abcdef",
    "correlation_id": "abcdef1234567890abcdef1234567890",
    "category": "upstream",
    "suggestion": "请核对网关厂商、地址、模型和输入参数，并用上游请求编号查询供应商记录；此分类不能确定上游内部原因。",
    "cause_known": false,
    "upstream": {
      "http_status": 400,
      "error_code": "InvalidParameter",
      "request_id": "f63f4568-9026-4b82-b076-05f92f92f711"
    }
  }
}
```

已有 `error.code`、`error.message`、`error.field` 保留，新增字段均为附加信息。分类为 `input/access/conflict/configuration/storage/upstream/interrupted/internal`。`suggestion` 是排查建议；`cause_known=false` 明确表示分类不能确定根因。明确的本地输入、版本或幂等冲突可为 true。

任务、剧本、分镜、创意任务、对齐和导出响应在已有持久 trace 可用时附加 `correlation_id`；失败时附加 `diagnostics`。历史任务没有追踪信息时不补造原始关联编号。任务正文仍含原有业务数据，应按原有权限保护，不应把整份任务当诊断日志上传。连续性检查的执行和网关日志也恢复其持久 trace；该领域原有列表/详情合同暂不增加内嵌诊断对象。

## 控制台与安全边界

API 和 worker 启动时设置 JSON 控制台格式。错误事件使用固定事件名和受控错误码，包含关联编号、分类；HTTP 事件包含方法、路由模板、状态和耗时，不记录实际路径、查询串、身份或请求正文。`startup_error` 的固定 `component` 区分配置、追踪、存储、执行、模型适配、认证、审计、监听等阶段；不序列化可能包含连接串或私有路径的原始异常。

网关管理接口由认证层提前处理，也会记录固定路由模板，例如 `/api/v1/ai-gateways/{id}/check`、`/api/v1/ai-gateways/{id}/sync-models`；认证失败时同样适用。无法匹配模板时记为 `unmatched`，不回显实际路径。`http_error` 在已有上游响应元数据时同步包含 `upstream_http_status`、`upstream_error_code`、`upstream_request_id`，可通过同一个 `correlation_id` 与 `upstream_error` 对照。

上游只提取状态、公开错误码和请求编号，不输出 `message`、原始响应、URL、请求头或提示词。公开错误码使用允许列表，未知码变为 `unrecognized:<指纹>`；UUID、常见十六进制编号可保留，其他编号变为 `sha256:<指纹>`。指纹只用于关联，不是授权凭据；非标准上游编号的指纹不能直接用于供应商控制台查询。旧调用记录在读取时同样执行脱敏，但不会重写历史文件或清除已存在的旧日志。

上游观察器最多暂存已被业务读取的 64 KiB，用于提取这些小字段；超出后释放缓冲。它不会为诊断额外读取、下载或重试，也不限制实际业务响应大小。上游未返回字段、正文未被消费、非 JSON、流式 SSE、超大或损坏响应时，保留能确认的状态/编号，原因保持未知。网关轮询记录表示最后一次观察，不是完整逐次响应历史。

目录检查与兼容文本适配器会显式读取至多 64 KiB + 1 字节的失败响应，用于错误处理及上述元数据提取，补齐原先 404、401/403、429 等分支未消费正文的问题；观察器自身仍不读取剩余正文。超大、HTML 或无错误码的响应不会被臆造为某个供应商错误码，`cause_known=false` 也不会仅因拿到 HTTP 状态就改成 true。

HTTP panic 在尚未提交响应时返回安全 500；流已提交时中断连接，不拼接伪造 JSON。日志最多记录仓库内函数名、文件名和行号，不记录 panic 值、完整堆栈或参数。供应商调用在工作流库格式化异常前捕获；结果未知不会被标记成功。PostgreSQL worker 释放执行槽并停止，保留已派发的恢复围栏；部署方仍须正常监督并重启 worker。文件队列内意外的非供应商回调异常可以被记录并释放槽，未能完成落盘的任务仍需按既有重启/恢复机制对账，不保证任意异常都立即终结任务。

私有审计文件保持既有身份与权限语义，控制台错误日志不会复制这些身份。指标专用端口和传统文本 404/405 等非 JSON 响应不强行改成新 JSON 合同。操作员 CLI 的输出合同不在本轮控制台改造范围内。日志采集平台应继续执行访问控制与保留策略。

## 已验证与未验证

确定性测试使用本地合成供应商和临时文件/数据库，覆盖拒绝响应、恶意敏感正文、panic、工作流异常、请求关联与 worker 派发围栏。准确执行结果及云端运行链接见[本轮规格与验收记录](superpowers/specs/2026-11-12-safe-error-diagnostics.md)。没有调用真实付费模型，没有更改生产配置或部署。

百炼 Token Plan 的账号额度、地域、实际权限及 `wan2.7-image` 现场拒绝原因仍需失败记录或供应商日志确认，不能由合成响应证明。参见[百炼排查记录](superpowers/specs/2026-11-12-bailian-image-diagnosis.md)。
