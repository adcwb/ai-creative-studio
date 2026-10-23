# 第七轮运行基础：已实现与未接入边界

本轮只本地实现与提交，不发布、不配置外部账号、不部署服务、不读取现有凭据。当前仍是单进程 FileStore；PostgreSQL、Redis、独立 worker、全量 S3 媒体存储和多实例 **尚未实现**，不是外部授权阻塞。`STORAGE_BACKEND` 仅接受 `file`，`QUEUE_BACKEND` 仅接受 `inprocess`，`MEDIA_BACKEND` 仅接受 `local`；未知值拒绝启动，绝不静默回退。

## 费用台账与多币种

`BudgetLedger` 保存 `budget.json`，在应用 DATA_DIR 排他锁内使用。金额全部为 `int64` 的币种最小单位，不使用浮点。默认 USD 不代表预算授权；没有显式 tenant/currency limit 就不能预留。支持 USD/EUR/GBP/CNY/CAD/AUD/CHF/HKD/SGD 两位最小单位、JPY/KRW 零位、KWD/BHD 三位；未知币种拒绝，不猜测精度。

预留按租户与币种原子扣减可用额度；相同 ID 相同输入幂等返回，换租户/币种/模型/金额拒绝。`reserved` 重启转 `unknown`，继续占用全额；超时、取消、失败和成功响应本身都不是免费或已结算证据。明确账单引用才允许 settle/release，最终状态不可改写。供应商实际超额仍完整记账，并阻止该币种新预留；这说明本地估计不是供应商账单硬上限。不同币种独立报表，不相加、不兑换。

离线管理员工具（停服、使用合成 tenant 演练）：

```sh
cd backend
go run ./cmd/budget -data-dir /path/to/stopped-data -tenant org-id -currency USD -action limit -amount-minor 1000
go run ./cmd/budget -data-dir /path/to/stopped-data -tenant org-id -currency USD -action report
```

`GetExchangeQuote` 接受可替换 provider；返回十进制字符串 rate、source、as_of、stale。缺少 provider 明确报未换算；过期报价保留 stale。没有默认或硬编码汇率，报价不会修改预算/账本。

**该台账尚未接入所有异步 provider worker/恢复路径，也没有用户费用管理 API/UI，因此不能宣称服务端完整计费已经完成。** `ValidateRuntimeFromEnv` 对 ready real provider 启动拒绝，保留无凭据真实模式的不可用能力展示和包内假上游协议测试。真实 smoke/eval 的执行仍须等待账本/内容策略接入与用户具体供应商、模型、预算授权。

## 即梦参考对象桥

`S3ReferenceBridge` 是可替换参考图复制接口，不是全量素材存储迁移。显式启用 `REFERENCE_BRIDGE_ENABLED=true` 并配置 `REFERENCE_S3_ENDPOINT/BUCKET/REGION/ACCESS_KEY/SECRET_KEY`（可选 SESSION_TOKEN）后才可使用。工具不创建存储桶、配置账号或持久凭据。

目标必须是固定 HTTPS origin，拒绝用户信息、非443端口、路径或查询参数。实际上传前验证所有 DNS 地址为公网地址，并固定拨号 IP，关闭代理、重定向和重试。验证图片后规范化 PNG，用随机对象键写入 `references/` 前缀；不设置 public ACL。返回只读300秒 SigV4 URL，不能写入客户端任务结果、日志或聊天。配置者必须提前设置存储桶访问控制和短期对象生命周期，URL过期不自动删除对象。失败上传可能留下对象；后续生命周期清理仍需运维部署时确认。

协议依据：[AWS S3 查询参数 SigV4](https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sigv4-query-string-auth.html)。测试使用官方公开合成凭据签名向量、本地模拟传输及恶意目标，不访问真实对象服务；真实 MinIO/S3 上传、供应商取图及效果仍未验证。

## 审计、指标与告警

`Operations.Wrap` 为请求分配随机 request ID，向本地 `audit.jsonl` 写入开始/结束时间、方法、响应类别和耗时。请求体、URL路径、query、Cookie、prompt均不记录。写入/fsync失败或超过64MiB容量后拒绝新的写请求；已发生响应后的审计失败无法撤销已完成业务，后续写请求会失败。停服后归档审计文件再重启，不自动删除证据。

这是请求级本地审计，不是具备用户/资源语义的不可篡改外部审计或分布式追踪。`MetricsHandler` 必须运行于独立运维端口，不能挂到公开Nginx代理后以回环peer冒充授权。导出HTTP计数、耗时总和、audit健康和uptime；`deploy/alerts.yaml` 是待操作者加载的告警配置，不表示已部署监控或通知渠道。

## 停服备份与恢复

```sh
python3 scripts/backup.py backup /path/to/stopped-data /private/backups/studio.tar.gz
python3 scripts/backup.py verify /private/backups/studio.tar.gz
python3 scripts/backup.py restore /private/backups/studio.tar.gz /path/to/new-data
```

备份获取与服务相同的非阻塞 `store.lock`，服务运行时拒绝。完整复制主项目、auth、budget、media、creative、alignment、导出与审计，不只备份state.json。拒绝符号链接/特殊文件，归档位于DATA_DIR外，已有归档和恢复目标拒绝覆盖。manifest逐文件SHA256和长度校验；恢复先验证全部路径/类型/重复项/校验和，再写入新0700目录与0600文件。归档包含账号哈希和会话等敏感数据，保持离线私有，不提交Git。校验和不是加密或签名，离线加密和远端保管尚需运维方案。

迁移回滚：停服创建完整备份，保留旧版本原DATA_DIR；新程序用完整复制的新目录演练。失败时停止新程序，旧二进制指向匹配的原目录，不能把新增字段/文件删掉后猜测兼容。恢复测试仅证明字节完整和恶意归档防护，恢复后的完整应用启动/媒体播放另需集成验收。

## 负载演练

`python3 scripts/local_load.py --base-url http://127.0.0.1:8080 --requests 100 --concurrency 4` 限定回环HTTP、无代理重定向、最多10000请求/32并发，仅GET健康端点，输出p50/p95/失败数。它不是创作worker、数据库、多实例或付费供应商吞吐认证。
