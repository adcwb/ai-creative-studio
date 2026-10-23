# Iteration 7 本地验收记录

日期：2026-10-23 UTC。生产实现冻结于 `ad05684e80a40c796819cb8931ae2919cadd1091`；随后唯一业务变化是英文分镜来源整句本地化 `348ab86`，已重跑前端检查及受影响浏览器验收。测试环境隔离及英语端到端覆盖提交为 `5e96e227117886757ad4eaea5d0ec86ceb6b9312`，也重新运行了受影响脚本。本文记录本次实际结果，不把旧发布版本的容器通过记录移作本轮证据。

环境：Go 1.25、Node 24、Chromium、FFmpeg/FFprobe；浏览器为桌面及 iPhone 13 尺寸的 Chromium 移动仿真，并非真实手机/Safari；全程临时本地数据、合成素材、mock 或本地模拟上游。没有读取现有凭据值、调用真实模型、推送代码、发布镜像或部署正式服务。

## 已通过

| 检查 | 命令/范围 | 本轮结果 |
| --- | --- | --- |
| Go | `gofmt` 检查；`go vet ./...`；`go test -race ./...`；`go build -o bin/server ./cmd/server` | 全包通过；server 1.086s，studio 82.108s；覆盖 session/CSRF、资源授权、跨租户、成员撤权、OIDC 本地模拟、预算预留/结算/未知/重启、审计、模型声明加密与拒绝 live 等 |
| 前端 | `npm run lint`、`npm run typecheck`、`npm test`、`npm run build` | 21 单测通过；最终英文来源修复后再次全过 |
| 翻译静态检查 | `node scripts/i18n_check.cjs` | 793 条中英消息；除语言本名外无未处理中文 JSX |
| OpenAPI | `openapi_spec_validator` 校验 `api/openapi.json` | 结构通过 |
| demo 契约 | `python scripts/contract_check.py` | 410 个实际 HTTP 响应符合 schema（含轮询，数量不是独立测试数） |
| secure 契约 | `python scripts/secure_contract_check.py` | 32 个实际响应；session/CSRF/租户、owner 预算/CAS/独立币种、成员撤权、FX 拒绝、模型声明/本地验证与 live 403 |
| 备份及测试隔离单测 | `python3 -m unittest discover -s scripts/tests -v` | 4 项通过：损坏/路径穿越、禁止覆盖、活动进程/符号链接，以及 Docker 所有调用仅用临时空凭据目录和本地 daemon |
| live 准备工具单测 | `python3 scripts/live_eval_test.py` | 5 项通过；默认不联网，拒绝缺授权/非法预算，未知费用保留跨重启，已有报告阻止重复付费提交 |
| 实际媒体 | `python scripts/media_check.py` | 7 组通过：真实上传/限制、幂等、时间轴、FFmpeg 完整解码/音轨/字幕/时长与信号、取消、实际重启恢复 |
| 高清实际编码 | Go `TestEnhancedMediaActualEncoding`（包含在全量 race） | FFmpeg 720×1280 烧录字幕像素、原视频音轨能量、黑场淡入帧、附加音轨；1920×1080 与 360×360 实际编码尺寸/字幕轨通过 |
| 字幕对齐 | `python scripts/alignment_check.py` | 4 组及 10 契约响应通过；逐句时间戳/SRT/VTT/真实 MP4 内嵌字幕、CAS/来源拒绝和重启；ASR 质量未验证 |
| 备份恢复/小负载 | `python3 scripts/recovery_check.py` | 完整恢复 4 个素材，内容 hash/元数据/幂等一致；运行中备份被拒绝。health 100 请求/并发 4，0 失败，p50 26.04ms/p95 87.63ms。仅健康端点，不能推论创作任务容量 |
| demo 浏览器 | `npm run test:e2e` | 62 项全部通过，5.1 分钟；桌面/手机，真实异步 API、丢响应幂等/冲突、草稿保护、成片播放、HD/烧录/音轨/转场设置、PNG mask alpha/新引用；英语图像/剧本/分镜/媒体、动态验证/日期/状态与切换持久 |
| secure 浏览器 | `npx playwright test -c playwright.auth.config.ts` | 8 项全部通过，15.9 秒；桌面/手机注册、session 刷新、组织/退出、私有媒体与匿名拒绝、独立 USD/CNY 预算、无凭据模型声明/本地验证、浏览器语言回退与持久切换 |
| 响应布局 | `node scripts/layout_check.mjs`、`project_check.mjs`、`storyboard_check.mjs` | 环境隔离和英文来源修复后 24 + 30 + 36 = 90 状态通过；短视口、长文本、控件可达与溢出检查 |
| 本地上游协议 | `node scripts/provider_check.mjs` | 5 次显式本地模拟上游调用，重启/中断后 0 自动重放 |
| 进程恢复 smoke | `python3 scripts/smoke.py` | 图片/剧本/分镜运行中 SIGKILL、receipt 恢复、失败保留旧版本、图片提供方不可用时剧本独立工作 |

## 测试环境隔离

Python 和 Node 脚本启动业务服务时仅继承 `PATH`，再添加每次测试明确的合成配置。Playwright 会合并父环境，因此配置先把父进程的全部变量名置空（不读取变量值），再设置 `PATH` 和测试配置。OIDC、KEK、参考对象桥接、模型命令、厂商凭据均不能隐式启用。demo 测试明确 `AUTH_MODE=demo`；身份与预算浏览器测试使用 secure 模式，临时本地注册，测试 cookie 仅限本地 HTTP。

Docker/S3 演练仅使用新建空 `DOCKER_CONFIG`，白名单环境不含 registry auth、`DOCKER_HOST`、`DOCKER_CONTEXT`、TLS 配置；不会读取现有 Docker 配置或连接配置中的远端 daemon。所有 MinIO 凭据均为一次性合成测试值。

## 明确阻塞/未验证

- **双容器当前未通过**：`python3 scripts/docker_check.py` 在构建拉取基础镜像阶段失败。Docker daemon 对 `auth.docker.io` 的 DNS 请求经 `172.31.3.125:53` 被拒绝；证据 `/tmp/studio-docker-evidence-cvrz1mv1/compose.log`。没有开始业务容器验收，未修改 daemon/网络设置，未读取 registry 凭据，未绕过 CI 发布门槛。
- **实际 S3 当前未通过**：隔离后的 `python3 scripts/s3_integration.py --image minio/minio:RELEASE.2025-09-07T16-13-09Z` 返回 `pull access denied`，退出 125，未创建服务容器。证据 `/tmp/studio-final-s3.log`。签名向量与 HTTP 模拟测试通过不等于真实对象存储互操作通过；Go 的 `TestS3LocalContainerIntegration` 无显式测试 endpoint 时跳过。
- **真实模型未运行**：USD 3 与 CNY 5 是分别批准的预算上限，不授权兑换/挪用/充值；缺少安全凭据和传输确认。候选模型兼容性与 live 执行仍有阻塞。工具的操作员预留值不是厂商计费硬上限，服务端实际模型入口仍 fail-closed。
- 本轮仍是单进程 JSON 存储。PostgreSQL/Redis/独立 worker/多实例事务与可恢复创作容量未完成，不能宣称生产就绪；详见交付矩阵及下一迭代计划。SSO 真实 IdP、内容审核执行强制门、真实强制对齐/口型/角色一致性模型质量未验收。

## 证据位置

本执行环境日志在 `/tmp/studio-final-*.log`，不是发布制品或持久外部链接。媒体证据 `/tmp/studio-media-evidence-8dou9u56`；字幕证据 `/tmp/studio-alignment-evidence-4ocugq57`；布局 `/tmp/studio-layout-evidence-so4zKM`、`/tmp/studio-project-evidence-RcmgJT`、`/tmp/studio-storyboard-evidence-ZCfi8x`；协议 `/tmp/studio-provider-evidence-vWUx79`。文中结果可由仓库脚本重建；没有保存现有凭据或真实个人素材。
