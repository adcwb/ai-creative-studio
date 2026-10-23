# 第八轮最终验证记录

代码冻结提交：`eab902453e6fb5ac5452bc47b604eeb44e709b79`。随后仅补充本验证记录、累计交付矩阵及修正文档中的即梦只读恢复说明，未修改产品代码。环境：Go 1.25.3、真实本地 PostgreSQL 17.11、系统 Chromium、真实 FFmpeg；没有真实供应商调用、推送、CI 发布或正式部署。

## 最终结果

| 检查 | 结果与范围 |
| --- | --- |
| Go 全量 | `go test -race ./... -count=1` 全通过；包含真实 PG 事务、跨实例/跨租户、撤权、费用未知、租约、追踪以及新空库备份恢复。studio 97.623s、queue 2.941s、tracing 1.040s。`go vet ./...`、全部 `cmd` build 通过。 |
| 前端 | lint、typecheck、23 单测、build、815 条 i18n 文案检查通过。 |
| 桌面／手机端到端 | 完整 62/62 通过，5.6 分钟；没有用此前 61/62 加定向重跑冒充完整通过。 |
| 布局 | 90 项通过：基础24、项目30、分镜36。 |
| API | OpenAPI 结构通过；435 个实际普通 API 响应及32个安全契约响应通过。轮询导致响应数量随运行耗时变化。 |
| 报价确认 | 真 PostgreSQL 桌面／手机 2/2：请求摘要报价→明确确认→接受任务。该测试不启动 worker，不是付费执行验收。 |
| PG worker 浏览器 | 真实 PostgreSQL＋独立worker，桌面／手机 10/10 通过；不能用单独报价2项替代。 |
| Provider 协议 | 5 次合成上游调用、0 自动重放；不是供应商真实账号或模型效果测试。 |
| 实际媒体／字幕 | 真实 FFmpeg 媒体7组通过；字幕4组及10个契约响应通过。 |
| 文件恢复／崩溃 | 4 个媒体资产的内容哈希、元数据、幂等恢复及 SIGKILL smoke 通过。 |
| PG＋对象完整恢复 | 完整 race 包含真实 pg_dump 快照、shared-file 对象校验、空库 pg_restore、unknown 任务/费用保留、活动租约/非空目标/损坏拒绝。 |
| 备份与工具单测 | 原备份/环境隔离4项、调度5项、live-eval本地工具5项通过。 |
| 配置 | CI、生产 Compose、告警 YAML 解析通过；生产 Compose 使用合成环境 `config --quiet` 通过，未启动服务。告警未用 promtool/规则引擎验证，也未部署。 |

## 真实多进程与有界创作负载

`scripts/multiservice_check.py` 使用本次创建并清理的独立 PostgreSQL 数据库、2 个 API、2 个 worker、各自私有 cache 和共享文件 BlobStore。通过图像、剧本、分镜、分析、字幕估算、实际 MP4 编码/解码；跨 API 幂等与素材读取；成员撤销与注销；在 dispatch 持久化之后 SIGKILL worker，再启动后保持 `unknown:1`，没有自动重新发送。

最终额外负载：两个租户、8个并发客户端、16个独立合成 SVG 创作任务，0失败；总9.605秒，接受延迟 p50 59.14ms / p95 225.14ms，完成延迟 p50 3197.15ms / p95 4767.44ms。证据：`/tmp/studio-multiservice-g2b52l0p/creative-load.json`。这是本地合成模型和该机器的工作负载，不是实际模型吞吐、S3服务或生产容量承诺。

## 环境问题与未通过项

- 首次冻结 race 的备份测试 URI 误加了 `passfile`，被备份工具安全 allowlist 拒绝；没有放宽校验。采用允许的 URI、新建 source/target 空库并保持凭据文件隔离后，完整 race 重跑通过。日志分别保留。
- PG 浏览器首次构建因本任务 Go cache 累积占满磁盘而失败。仅清理可重建的本任务缓存，保留源码、业务数据与证据后原样重跑；没有修改测试绕过。
- Docker 正常官方拉取重试失败：`nginx:stable-alpine` 请求 `auth.docker.io` 匿名 token 时，DNS `172.31.3.125:53` 返回 connection refused。证据 `/tmp/studio-docker-evidence-f7p_tfl7/compose.log`。未改安全网络配置、未登录 DockerHub。本轮双容器未验证；既有 container-smoke 发布门槛保留，并新增 durable-smoke。
- 实际独立 rclone S3 服务不满足合同：条件写覆盖已有对象、Range 返回200。没有放宽不可变写或用 HEAD+PUT 假装原子；MinIO官方二进制下载另返回403。S3协议fixture通过不等于实际对象服务通过，详见[对象存储验证](iteration-8-object-storage.md)。
- 真实模型、SSO、外部审核/账单/汇率、外部追踪/告警、异地容灾及目标部署容量未验证。USD3/CNY5预算未使用；凭据与认证发送确认仍缺失，live保持关闭。

## 复现与证据

逐项机器可读汇总见 [测试报告](iteration-8-test-report.json)。PG worker 浏览器证据 `/tmp/studio-postgres-browser-rn_pyeup`，实际媒体 `/tmp/studio-media-evidence-yrplu5m4`，字幕 `/tmp/studio-alignment-evidence-1e4vjrua`。

完整 race 日志：`/tmp/studio-iteration8-frozen-race-retry.log`；E2E：`/tmp/studio-iteration8-frozen-e2e.log`；Provider：`/tmp/studio-provider-evidence-9yELi4`。这些临时证据属于本次环境；仓库保留测试脚本和本记录，不能把临时路径当作永久制品存储。

配置真实本地测试数据库时用 `STUDIO_POSTGRES_TEST_DSN`、`JOBQUEUE_TEST_DSN`、`STUDIO_BLOB_TEST_DSN`、`STUDIO_WORKER_TEST_DSN`、`STUDIO_AUDIT_TEST_DSN`，原生恢复另设 `STUDIO_POSTGRES_BIN_DIR`。完整备份需要本次专用的 `STUDIO_BACKUP_TEST_DSN` 与 **全新空** `STUDIO_BACKUP_TARGET_DSN`（显式 PostgreSQL URI，参数遵循工具 allowlist），以及 `STUDIO_TEST_PG_DUMP`/`STUDIO_TEST_PG_RESTORE`。绝不把既有生产库用于这些破坏性 fixture 测试。

`scripts/multiservice_check.py` 与 `scripts/postgres_browser_check.py` 支持明确的 `--pg-bin`、`--pg-socket`、`--pg-port`、`--pg-user`，创建随机命名测试库并只清理自己创建的库；后者加 `--quote-fixture` 运行单独报价测试。备份操作步骤与回滚边界见[数据库迁移](iteration-8-postgres.md)、[对象迁移](iteration-8-object-storage.md)、[完整备份](postgres-backup.md)。
