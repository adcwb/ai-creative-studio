# 第九轮最终验证

日期：2026-10-26。最终产品提交 `c09d37b2df5965693318299745ba4a9dd30a7088`；Go及前端在 `f698afd6b3270a56574412f3e80672d7ea67d9c7` 冻结，之后仅新增独立本地模型Python。最终回归后只补CI与文档，不改变已测产品代码。

**33组检查：32通过，1外部阻塞；无产品测试失败。** 完整逐项退出码、时间、日志和未运行项目见 [机器可读报告](iteration-9-test-report.json)。没有真实供应商请求、付费、推送、发布或部署，USD3/CNY5未使用。

| 范围 | 最终结果 |
| --- | --- |
| Go | gofmt、vet、完整`go test -race ./... -count=1`、全部cmd构建及server构建通过。studio126.153s、queue3.128s、securebackup1.430s；完整race含编译165.672s。 |
| Docker相同静态参数构建 | `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' ./cmd/...`通过31.552s，含backup与model-key-rotate；不是双容器验收。 |
| 前端 | lint/typecheck、30单测、build、838条i18n检查通过。 |
| 浏览器 | 普通桌面/手机64项通过，另安全认证桌面/手机10项通过；PG独立worker10项、精确报价2项通过。 |
| 布局 | 基础24＋项目30＋分镜36，共90状态通过。 |
| API | OpenAPI结构、430个普通实际HTTP响应、32个安全契约响应通过；轮询响应数随耗时变化。 |
| 真实媒体 | 7组媒体、4组字幕及10个字幕契约响应通过；新增crossfade像素/PCM/字幕/时长及overlay层顺序/透明度/原音此前专项race通过，最终全race再次包含。 |
| 模型协议 | Provider合成集成通过；managed eval20项Python测试通过，另真实PG/API接收链验证配置、报价、预留和撤销。该演练不启动worker，供应商请求为0。 |
| 恢复与轮换 | 文件4素材恢复、进程SIGKILL、原生pg_dump/pg_restore、加密包seal/open/空库恢复、unknown费用保持、文件/PG合成KEK轮换及错误/撤销/回滚全部纳入最终检查通过。 |
| Python | 原备份/环境4项、加密调度7项、managed eval20项、具体离线模型12项和py_compile通过。 |
| 本地模型验证含义 | 具体CTC算法、引擎调用协议、实际FFmpeg、哈希/用途/边界/资源限制和进程网络拒绝；没有安装真实权重或运行模型推理质量测试。 |

## 真实多服务合成与负载

最终 `scripts/multiservice_check.py` 使用新建、独立的PostgreSQL17.11数据库，2 API＋2 worker、每实例私有cache与shared-file BlobStore。通过旧六类工作流、crossfade与叠层真实导出/像素/时长、仅叠层使用的素材在worker独立cache中物化、跨租户拒绝、撤权/注销、幂等以及dispatch后SIGKILL保持unknown而不重发。

两个租户、8并发客户端、16个独立合成任务零失败；接受p95 **428.19ms**，完成p95 **4766.73ms**。证据 `/tmp/studio-multiservice-uhryvkyq`。这是本机合成SVG模型与真实媒体链路验证，不是远端模型、实际S3或生产容量证明。

managed工具实服务证据 `/tmp/studio-managed-eval-8bd_u6u0`：固定合成账号、KEK、报价与deny-only审核，仅API运行。真实模型配置ID/版本、catalog、治理预算、quote和creative接收合同通过；数据库保持queued，费用预留不凭空结算，供应商调用0。

## 失败、未运行与外部阻塞

- **Docker构建失败，双容器未运行**：正常官方匿名基础镜像拉取 `debian:bookworm-slim`，请求 `https://auth.docker.io/token` 时经 `172.31.3.77:53` DNS查询出现 `i/o timeout`。日志 `/tmp/studio-docker-evidence-2hkr8o1e/compose.log`。没有读取Docker登录配置、改DNS/防火墙/daemon网络或降低发布门槛。此项不是产品测试失败，也不能标为通过。
- **真实供应商推理/付费验收未运行**：预算不替代凭据、认证发送和模型规格确认。未替用户配置真实key或账户。
- **真实本地权重推理与质量未运行**：Wav2Vec2英文对齐、Wav2Lip及AudioGen具体桥接有代码和离线协议验证，不代表依赖/权重安装、中文对齐、口型效果、音效语义或CPU性能已达标。源码、权重hash、用途许可和资源须另核。
- **实际S3本轮未运行**：仍无合格独立对象服务验收结果；此前rclone条件写/Range不兼容不被fixture通过覆盖。
- **生产部署/容量未运行**：外部SSO、审核/账单/汇率服务、告警通知、collector、异地灾备和正式环境故障域不在本次已通过范围。

## 复现和工具链

Go1.25.3由官方清单与发行archive SHA256核验恢复；PG17.11从Debian官方包按Packages摘要核验，仅用于新合成cluster。恢复过程中未修改宿主网络安全配置，也未切换环境/模型绕额度。先前直接DNS失败不再冒充当前Go阻塞；本轮续做未再触发开发额度错误。

完整Go集成设置 `STUDIO_POSTGRES_TEST_DSN`、`JOBQUEUE_TEST_DSN`、`STUDIO_BLOB_TEST_DSN`、`STUDIO_WORKER_TEST_DSN`、`STUDIO_AUDIT_TEST_DSN` 到隔离测试库。备份另用全新 `STUDIO_BACKUP_TEST_DSN`/`STUDIO_BACKUP_TARGET_DSN` URI及 `STUDIO_TEST_PG_DUMP`/`STUDIO_TEST_PG_RESTORE`，禁止指向生产库；备份URI不支持passfile引用，辅助进程自行使用空凭据文件。原生恢复另配 `STUDIO_POSTGRES_BIN_DIR`。

多服务、PG浏览器、managed eval脚本使用明确 `--pg-bin/--pg-socket/--pg-port/--pg-user` 创建自己的随机测试库。新增managed接收演练和本地模型离线测试已纳入CI门槛；CI没有在本轮触发。临时详细日志为 `/tmp/studio-qa9-final-*.log`，不是永久制品托管；此记录和JSON汇总随仓库本地提交保存。

迁移与回滚：[新时间轴](iteration-9-media.md)、[数据库](iteration-8-postgres.md)、[完整备份](postgres-backup.md)、[加密保留](backup-encryption-retention.md)、[KEK轮换](model-key-rotation.md)。恢复旧二进制须匹配数据备份；轮换不得复活撤销记录，结果不明先停写核对，不自动重试。
