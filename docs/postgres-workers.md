# PostgreSQL 队列与独立执行器

应用选择 PostgreSQL 作为外部持久队列，不额外依赖 Redis。队列行与领域记录、接受时的金额预留在同一数据库事务内提交；因此没有 DB→Redis 双写窗口，也无需把 Redis 可用性当作接单条件。200ms 轮询与 `FOR UPDATE SKIP LOCKED` 支持多个执行器争抢，数据库是唯一任务事实源。这不是供应商 exactly-once 保证。

## 启动

先用 `store-migrate --mode init --confirm-stopped` 显式初始化 schema。API 与 worker 的部署账户不应拥有 DDL 权限；自动 DDL 仅在明确设置 `STORAGE_AUTO_MIGRATE=true` 时运行，供隔离测试使用。

```sh
# 使用部署注入的 DATABASE_URL；不要把含密码的 URL 写进命令历史或版本库。
STORAGE_BACKEND=postgres QUEUE_BACKEND=postgres DATA_DIR=/private/worker-cache ./worker
```

worker 不监听 HTTP。它加载与 API 一致的本地/演示适配默认值、对象存储、治理策略、模型配置和部署 KEK。模型凭据只在受授权执行时解密；不在启动时发送供应商探测。托管远端任务仍需有效模型版本、当前成员权限、明确币种/金额上限、未过期报价和审核；未受治理的环境变量远端执行继续拒绝。

目前独立消费 image、script、storyboard、creative、alignment、export，以及显式请求的 image_resume/creative_resume。API 的 PostgreSQL 模式不运行这些本地 goroutine。跨主机必须配置共享 S3 BlobStore；仅共享 PostgreSQL 不会让不同主机的本地媒体缓存自动同步。

## 恢复与取消

- 接受事务失败时，领域记录、队列和资金预留均回滚。幂等重放不能改变原 payload。
- 每次领取产生随机 lease token；租约续期、上游 ID 持久化、素材导入及最终结果提交都检查 token。晚到的旧 worker 不能提交领域结果。
- `dispatch_started` 在任何模型执行或本地渲染之前持久化。此状态租约过期后变为 `unknown`，不会自动重新提交；领域状态由恢复协调器转为明确失败/结果未知，原上游 ID 和费用占用保留。
- 取消在领域事务内撤销 queue token。尚未发送且仍为 reserved 的资金可以释放；已经开始发送或费用不确定的预留不能因取消释放。
- 上游 ACK 持久化失败时，保留未知状态，不能以“没保存成功”为由重发创建请求。
- 显式恢复只允许已有上游 ID、有效旧模型配置版本、当前授权、原 unknown 费用记录，以及部署策略明确记录来源/时效的免费查询协议。恢复不会新建生成任务或重复预留原费用，也不会把成功查询推断为免费生成。
- 只读恢复 transport 拒绝创建请求；即梦仅允许特定官方查询 action/payload，其他 POST 不因“恢复”被放行。旧退役接口仍拒绝。

## 实际验证与边界

`JOBQUEUE_TEST_DSN` 指向专用合成测试库时，`go test -race ./internal/jobqueue` 实测跨进程竞争、dispatch 后进程退出、租约失效、旧 token 拒绝、事务回滚和跨租户访问。`STUDIO_WORKER_TEST_DSN` 启用 `TestPostgresQueuedCreationAndWorkers`：两个独立 store/cache、共享 BlobStore、六条创作执行链和真实 FFmpeg 导出。

上述测试不代表供应商 live 调用或 S3 服务互操作已经验证。实际 API/worker 多进程、恢复与对象服务结果应以最终验收记录为准。队列按 kind 分别消费，`WORKER_CONCURRENCY` 控制单进程共享执行槽位（默认 2，严格 1–16）；等待槽位的任务仍续租，但尚不进入 dispatch。PostgreSQL 领取事务使用全局 advisory 锁计数，所有实例合计最多 8 个有效租约、每租户最多 4 个；export/alignment 每种最多 2 个，其他种类最多 4 个。已过期的租约不占槽位，已 dispatch 的工作仍禁止重发。这些是任务数量约束，不是共享 CPU、显存或对象存储字节配额；具体创作容量须在目标资源限制下测量。未知费用必须由有证据的账单核对解决，不能自动归零。
