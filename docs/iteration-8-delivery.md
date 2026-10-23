# 第八轮：生产执行链与持久化

从本地 `6895cdf47acddd4b620fb679f580c23c8224edca` 继续；远端 master 再次只读核对仍为 `5c6060b09d3cc7d054e57f5b0dc7dfcf3c98fe7d`。本轮没有推送、发布镜像、正式部署、读取已有供应商 secret 值或执行真实模型请求。已批准 USD3/CNY5 的首轮小样额度没有使用。

## 已落实的执行链

| 范围 | 本轮实现 | 验收边界 |
| --- | --- | --- |
| 管理配置应用 | 任务接收绑定真实 actor/tenant、配置 ID/版本、能力与费用确认；发送前重新授权、按租户解密、固定官方 adapter；配置/成员撤销中止后续发送并取消在途请求 | 在途请求取消不等于供应商未计费；实际账号未验证 |
| 费用与审核 | 统一执行治理，原币种整数预留、dispatch 前持久 unknown、可信证据结算/释放、无证据保留；输入和产出审核；已知任务仅受控查询恢复 | 必须部署明确报价、审核与账单适配，缺失拒绝；无默认 allow 或假价格 |
| PostgreSQL | 业务、身份、会话、成员、治理、模型配置、媒体元数据实际 SQL 读写；每域事务、版本/幂等、租户关系约束；共享 OIDC 一次性状态和登录限流 | 当前按领域锁串行刷新记录，吞吐不是细粒度仓库实现；不宣称无限横向扩展 |
| 外部队列 | PostgreSQL 直接持久队列，接受记录/费用预留/队列在同事务；SKIP LOCKED、随机租约 token、fencing、unknown 恢复 | 选择数据库队列替代 Redis，无双写/outbox 消息窗口；不承诺供应商 exactly-once |
| 独立 worker | 图像、剧本、分镜、创作、对齐、FFmpeg 导出及已知任务恢复由独立进程消费；PG API 不启动这些执行 goroutine | 合成协议、真实本地媒体及进程恢复验证，不是模型质量验收 |
| 多实例 | 两 API/两 worker、共享权限/会话/模型/预算/任务与对象索引；跨实例撤权、注销、幂等和结果读取 | 跨主机需真实共享存储；共享文件演练不等于 S3 服务验证 |
| 对象存储 | File/S3 BlobStore，PNG/全部素材/MP4/SRT/VTT 的不可变写、hash、Range、授权下载、私有本地物化；SQL manifest | 128MiB 单对象上限；无自动孤儿 GC、cache 淘汰、bucket 总配额或异地复制 |
| 审计与故障 | PostgreSQL 共享持久审计，事务同步 WAL；不存请求正文、cookie、token 或 URL；DB 失败不能伪装为空列表成功 | 审计不是外部不可篡改归档；完成记录不替代业务事务提交证明 |
| 迁移恢复 | 显式离线双向元数据迁移、空目标要求、摘要与计数；快照绑定 pg_dump＋全部引用媒体的完整备份包，真实空库恢复及 unknown 费用保留；对象独立迁移回滚 | 全部 API/worker 必须停写，原生 PG 工具需另供应；媒体与 KEK 需匹配保管 |
| 备份调度 | 可执行 pre/post hook runner、全程独占锁、有界超时，systemd service/timer 模板及合成调度演练 | 模板未安装；操作者须提供停写/恢复 hook，无自动异地复制、加密托管或已验证 RTO/RPO |
| 追踪与告警 | HTTP→持久队列→worker→治理/provider 的隐私白名单 spans；默认关闭、有界本地 JSON exporter；队列 unknown/DB/审计/trace-loss 指标与告警配置 | 非托管追踪或已部署告警；没有 OTLP 传输、通知目的地或外部 collector 验证 |

接口与操作说明见 [模型运行时选择](runtime-model-selection.md)、[费用审核治理](execution-governance.md)、[数据库迁移](iteration-8-postgres.md)、[独立 worker](postgres-workers.md)、[对象存储](iteration-8-object-storage.md)、[完整备份与调度](postgres-backup.md)、[追踪与告警](tracing.md)。第七轮交付矩阵作为历史快照保留，其中数据库/worker/runtime 缺失项已由本文件更新。

## 尚未完成或尚未验证

- 真实模型、正式 SSO、供应商审核/账单及真实汇率源仍未配置或验收；没有从测试预算推导出读取或持久化真实凭据的授权。
- 实际 S3 兼容性以服务合同测试为准；HTTP 方法相似不代表支持原子条件写。不能用 HEAD 后 PUT 替代 `If-None-Match` 的并发保证。
- 部署中的告警通知、外部 trace collector、自动异地备份/容灾、外部不可篡改审计、生产容量与故障域演练尚未验证。已完成的本地创作负载只使用合成 SVG 模型，不能推论真实模型或目标集群容量。
- 第七轮创作边界保持：黑场淡入淡出不是交叉溶解或视频叠层；角色检查不保证锁脸；本地强制对齐/口型/音效模型尚未安装或质量验证；即梦视频无已确认可用官方接口；退役 OpenAI Videos 继续拒绝。
- 正式资源、域名、账户、付费服务和部署未执行。不能将本轮代码和合成测试称为生产就绪。

## 最终验收

最终代码冻结后的完整结果另记入 `iteration-8-verification.md`。此前的中间通过不代替最后改动后的完整回归。原 Docker/发布门槛保留，新增 PostgreSQL durable-smoke 门槛；本地不触发 CI 或镜像发布。
