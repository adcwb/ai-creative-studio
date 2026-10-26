> 后续第九轮已继续落实音乐治理、备份认证加密／保留、离线KEK轮换及候选评测接线；最新状态与未验证项见 [第九轮矩阵](iteration-9-delivery.md)。第八轮的生产架构取舍及性能界限仍适用。

# 第八轮生产实现状态与后续边界

第七轮留下的模型运行时、执行费用/审核、PostgreSQL、外部队列、独立worker及完整BlobStore已继续落实，当前矩阵见 [第八轮交付](iteration-8-delivery.md)。以下原阶段拆分保留用于解释迁移约束，不再表示这些模块全部尚未实现。

| 原阶段 | 当前结果 | 继续保留的限制 |
| --- | --- | --- |
| A 领域边界 | BlobStore与SQL持久层实际接线，文件模式兼容 | 部分HTTP仍使用历史FileStore类型作为应用容器；尚未完成细粒度repository重构 |
| B PostgreSQL | 业务/身份/治理按领域记录SQL读写，可靠事务/CAS/租户约束，显式双向离线迁移 | 当前领域范围锁与全域刷新限制吞吐；不是逐实体锁的高容量实现 |
| C worker与对象 | PostgreSQL直接外部队列，任务+预留原子接受，八类独立worker，fencing/unknown恢复，全媒体BlobStore | 选择DB队列替代Redis，实际S3服务兼容性尚未通过；不承诺供应商exactly-once |
| D 多实例 | 真实两API/两worker、共享会话/撤权/幂等/费用/对象索引，以及实际媒体和kill恢复验证 | 只证明此本地工作负载；目标集群容量、真实模型及云对象服务须另验收 |

后续不能忽略真实供应商审核/账单/价格配置、实际S3合同测试、部署环境负载和故障域演练。追踪与备份的实现及验证以最新矩阵为准；外部告警通知、异地复制和正式上线尚未执行。

---

## 第七轮原实施拆分（历史）

以下为第七轮时的未实现项拆分，不是当前状态；当时冻结增量与证据见[交付矩阵](iteration-7-delivery.md)。数据库、Redis、独立worker和多实例并非被模型凭据阻塞；它们需要继续代码实现与本地设施验收。

| 阶段 | 必须真实实施 | 通过条件 |
| --- | --- | --- |
| A 领域接口 | Application依赖容器、领域事务接口、BlobStore；FileStore先实现行为兼容 | HTTP不再依赖`*FileStore`断言，现有全流程通过 |
| B PostgreSQL | 领域记录表、租户复合外键、版本/CAS、幂等/预算原子事务、离线迁移 | 元数据实际读写DB；迁移摘要与恢复一致，不先宣称多实例 |
| C worker与对象 | 统一任务表、事务outbox、Redis通知、租约执行凭证、S3 BlobStore、独立worker | API不执行生成/合成；重复投递/崩溃不重复付费提交 |
| D 多实例 | 共享会话/OIDC一次性状态/限流、跨进程取消与任务租约 | 两API/两worker与DB/Redis/S3实际并发、故障、恢复、跨租户验收 |

## 第七轮耦合记录（历史）

`http.go`/`project_http.go`按`*FileStore`断言注册部分路由；creative worker直接依赖FileStore；媒体导出与alignment接受请求即启动goroutine。AuthStore登录尝试、OIDC pending和取消函数是进程map。仅配置Postgres/Redis镜像或把JSON镜像写入数据库，都不能解决这些问题。

`AcceptGeneration`应在一个事务内完成归属/版本、幂等、活动任务限制、币种预算预留、任务与outbox创建，不能分别写多个仓库后宣称原子。每领域内容可使用每记录JSONB，但tenant、ID、version、status、关联及金额应有数据库约束。

## 第七轮设计约束（本轮采用 PostgreSQL 直接队列）

迁移只读锁定旧完整目录，导入空目标schema，保留ID、版本、幂等、来源和上游ID。未知费用继续占用预算；旧demo资产不自动分配账号。核对记录数、摘要和素材哈希后才切换。禁止未经完整双写协议就承诺文件/数据库随时无损切换；切换后的新写入需要反向迁移，回滚必须明确停写点。

数据库作为任务事实源，Redis只通知；outbox提交后投递，丢消息能重建。worker执行凭证阻止过期worker提交结果。远端生成发送前持久`dispatch_started`：之后崩溃不能自动重新提交；有上游ID只能查询，没有ID标费用/结果未知。租约不等于供应商exactly-once。

BlobStore至少提供Put/Stat/OpenRange/Delete及受控本地物化。FFmpeg只处理校验、有界的私有临时文件，下载始终先授权，不暴露存储凭据。

## 原验收要求（Redis 项已由 PostgreSQL 队列故障测试替代）

同版本跨API竞争、幂等重放、双worker竞争、重复与丢失队列消息、Redis重启、各关键提交点崩溃；跨实例注销/撤权/OIDC回调；跨租户对象与金额隔离；DB恢复、对象缺失/损坏、实际媒体与有界创作负载。全部通过后才更新能力矩阵，不能用健康请求负载或mock接口代替。
