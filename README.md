# 映创 · AI Creative Studio

React / TypeScript / Vite 前端与 Go / Eino 后端分离的视觉创作工作室。支持图像任务、项目简报、剧本、分镜、素材、历史、时间轴、字幕、混音与 MP4。默认模拟素材始终保留来源；假上游与合成媒体验证不代表真实模型质量或费用验证。

本轮基线为 `5c6060b09d3cc7d054e57f5b0dc7dfcf3c98fe7d`。**本轮只本地提交，不推送、不触发镜像发布、不部署。** 已获批首轮测试预算 USD 3 与 CNY 5，分别封顶、不兑换、不充值；凭据与认证传输确认仍未就绪，没有执行真实模型调用。当前补缺实现与验收见 [第九轮交付矩阵](docs/iteration-9-delivery.md)；生产基础见 [第八轮交付矩阵](docs/iteration-8-delivery.md)，[第七轮矩阵](docs/iteration-7-delivery.md)保留历史快照。

## 当前能力与边界

- 默认安全模式：注册开关、密码哈希、HttpOnly 会话、CSRF、组织成员与 owner/admin/editor/viewer 服务端授权。资源读写、下载和幂等记录按组织隔离；旧演示数据不会自动归给新账户。OIDC 支持授权码、PKCE、state、nonce 和签名校验；外部 SSO 尚未配置或验证。[身份与迁移](docs/authentication.md)
- 界面支持简体中文和英文、持久化语言选择与浏览器语言回退；日期和数字按语言格式化。币种独立于语言，默认 USD；预算账本按币种整数最小单位隔离，不做隐式换算。[国际化](docs/internationalization.md)
- 组织 owner 配置原币种预算；托管模型执行接入预留、dispatch、审核和费用证据处理，未知费用继续占用。缺少明确报价、审核或授权时拒绝执行；旧全局远端环境配置不能绕过治理。[执行治理](docs/execution-governance.md) / [预算 API](docs/iteration-7-governance-api.md)
- 保留原子文件模式，新增 PostgreSQL 业务/身份/治理存储与数据库任务队列、独立 worker、共享对象存储及多实例授权。采用数据库直接队列替代 Redis 双写；实际两 API/两 worker 与本地媒体演练通过。部署与容量仍需专门验收。[独立 worker](docs/postgres-workers.md) / [对象存储](docs/iteration-8-object-storage.md)
- 360p / 720p / 1080p、16:9 / 9:16 / 1:1、软字幕或烧录、源视频音轨显式保留与混音、最多 8 条定位音轨、黑场淡入淡出、交叉溶解及最多 4 层图片／视频叠加。[合成规格与限制](docs/iteration-9-media.md)
- mask 绘制与 OpenAI 局部编辑协议、Google 首帧与视频编辑、即梦短期对象 URL 桥接、双图角色一致性反馈、MiniMax 存量账户器乐协议，以及可替换本地强制对齐／口型／音效适配。另提供[具体离线引擎桥接](docs/local-model-engines.md)：英文 Wav2Vec2 CTC、Wav2Lip、AudioGen。尚未安装模型权重或验证真实推理质量；后两者受非商用用途限制，默认不启用。人物反馈不保证锁脸。[适配与官方依据](docs/iteration-7-provider-enhancements.md)

模型接入管理支持租户管理与脱敏配置；配置验证与真实请求分开。凭据只能服务端加密或引用允许的 secret 名称，部署密钥没有默认值。用户必须显式选择配置版本并确认原币种费用上限；worker 再次授权、解密并应用配置，撤销会阻止继续发送。保存默认模型不会把原模拟操作静默变成收费请求，管理页面也不代表已取得供应商权限。[运行时选择](docs/runtime-model-selection.md)

## 供应商状态

下表表示代码与协议状态，所有真实账号、效果、耗时和费用均未验证。具体模型是否兼容仍需单独核对，消费产品名称不等于 API 模型 ID。

| 官方生态 | 已实现协议 | 明确限制 |
| --- | --- | --- |
| OpenAI | 文本、视觉、图像、参考与 mask、语音、Whisper 时间戳 | Videos/Sora API 已退役，本地拒绝；旧语音接口已标弃用；gpt-transcribe 不等于现有 whisper-1 对齐协议 |
| Anthropic / Claude | Messages 文本与视觉 | 无图像／视频／语音生成；新型号参数须适配 |
| Google / Gemini / Nano Banana | generateContent 与 Interactions 图像、文本／视觉、Interactions 视频首帧／编辑与语音 | 使用支持相应协议的显式型号；不回退到已关闭旧 preview |
| 字节 / Ark / 豆包 | 文本／视觉、图像／参考、视频首帧、独立豆包 TTS | 方舟与豆包 TTS 权限和凭据不同 |
| MiniMax | 文本／视觉、图像人物参考、视频首帧、语音、器乐 | 音乐仅明确确认的存量获权账户；视频规格不能只换型号就假定支持 |
| 即梦公开视觉 API | AK/SK 图像签名、查询恢复、受控 S3 参考桥接 | 无已核实可用新版视频接口；不逆向消费网站 |

即梦参考桥接只向部署明确配置的 S3 兼容存储上传受控参考图，使用短期签名 URL；没有创建公网资源或验证公网访问。参考桥接与通用 BlobStore 独立配置；实际 S3 服务互操作仍需单独验收。

## 本地运行

需要 Go 1.25.3、Node.js 24 / npm 11、FFmpeg / FFprobe。后端配置仅从启动进程环境读取，不自动读取 `.env`。

安全模式开发示例（仅本机 HTTP，用全新测试账号）：

```bash
cd backend
AUTH_ALLOW_REGISTRATION=true AUTH_COOKIE_SECURE=false go run ./cmd/server
# 另一终端
cd frontend
npm ci
npm run dev
```

前端 `http://localhost:5173`，后端回环 `http://localhost:8080`。默认注册关闭，Secure cookie 开启；正式 TLS 配置不可照搬本地 HTTP 开关。没有创建或配置用户已有账号。

若仅复现旧无身份演示，显式设置 `AUTH_MODE=demo`。该模式公开两个演示空间，`space_id` 不是安全边界，不能暴露公网或使用真实客户数据。默认安全模式不会把演示空间冒充租户。

Docker 使用 `docker compose build` 和 `docker compose up -d`；前端仅绑定回环，Nginx 同源代理，后端包含 FFmpeg 与字体。安全注册、cookie 和 TLS 按 [身份说明](docs/authentication.md) 及 [Docker说明](docs/docker.md) 配置。本轮环境容器结果以交付矩阵为准，旧 CI 成功不算本轮通过。

## 创作到成片

新建项目，保存简报，生成模拟剧本和分镜。在“素材与成片”上传 PNG/JPEG、WAV/MP3 或 H.264 MP4，或创建明确标注的色块／正弦音演示素材。选择镜头顺序、入点、字幕、音轨与输出规格，保存时间轴，再导出 MP4。

导出为 H.264/AAC、24fps。360p 最长180秒、720p最长120秒、1080p最长60秒；最多30镜，每镜0.5–30秒。上传单文件32MiB，图片最多2000万像素；导出另有并发、容量与128MiB产物限制，以服务端为准。转场可选黑场淡入淡出或向下一镜交叉溶解，后者缩短总时长并同步字幕／音轨时间。最多4层画面可定位、调整透明度并显式混入原音；没有实时叠层预览或关键帧，不宣称专业NLE。

字幕支持人工逐句、文本估算、ASR 候选和可替换强制对齐。估算不识别音频，ASR 不等于已知对白强制对齐；候选须复核后应用并保存。源视频原音默认不保留，必须显式启用。配置或模拟测试不能证明角色一致性与口型质量。

## 数据、迁移与恢复

默认文件模式主快照仍为兼容 v1/v2 升级的 v3 JSON，其他媒体、创作、身份、预算与配置文件位于同一 `DATA_DIR`。必须备份整个目录；不是只备份 `state.json`。文件锁阻止同目录双实例，损坏状态拒绝启动，不清空重建。

使用 `python3 scripts/backup.py --help` 查看停服备份、校验和恢复操作；恢复至新目录，验证后再切换。新增身份不会自动认领旧演示资产；回滚旧程序应恢复其匹配版本完整备份，而不是修改快照版本号。加密凭据恢复还需要部署方安全保管的原 KEK，备份包不包含该环境密钥。第九轮增加[备份认证加密与保留](docs/backup-encryption-retention.md)和[离线KEK轮换](docs/model-key-rotation.md)，仅用合成密钥演练；真实密钥仍由部署方安全管理。

PostgreSQL 模式使用 `STORAGE_BACKEND=postgres`、`QUEUE_BACKEND=postgres`、部署注入的 `DATABASE_URL` 与显式共享 BlobStore；API 和 worker 使用各自私有 `DATA_DIR`。先离线执行 `store-migrate --mode init --confirm-stopped`，启动不默认建表。数据迁移及反向恢复必须停写、使用空目标并匹配媒体与 KEK，不能只改环境变量承诺回滚。[数据库＋媒体备份包与调度](docs/postgres-backup.md)要求停止全部 API/worker，并提供匹配 PostgreSQL 客户端；模板未安装。可选[跨进程追踪与告警配置](docs/tracing.md)默认关闭，只导出有界脱敏数据。真实数据库恢复与多进程演练覆盖范围见第八轮矩阵；没有承诺异地容灾或生产容量。

## 检查与验收

```bash
cd backend
test -z "$(gofmt -l cmd internal)"
go vet ./...
go test -race ./...
go build -o bin/server ./cmd/server
cd ../frontend
npm ci
npm run lint
npm run typecheck
npm test
npm run build
PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/usr/bin/chromium npm run test:e2e
```

其他验证从根目录执行：

```bash
python3 scripts/smoke.py
python3 scripts/contract_check.py
python3 scripts/media_check.py
python3 scripts/alignment_check.py
python3 scripts/recovery_check.py
node scripts/provider_check.mjs
node scripts/layout_check.mjs
node scripts/project_check.mjs
node scripts/storyboard_check.mjs
python3 scripts/docker_check.py
```

OpenAPI 使用 `openapi-spec-validator==0.7.2`；安全身份桌面/手机使用 `frontend/playwright.auth.config.ts`。测试使用隔离临时目录、明确 demo 模式或合成账号、合成媒体及本地假上游，不依赖真实凭据。

真实小样准备见 [验收工具](docs/live-evaluation.md) 与 `eval/candidate-plan.json`。当前候选存在 adapter／规格不匹配项目，工具默认 dry-run；缺安全凭据、准确上限或发送授权时不能执行。未知创建结果保留费用预留并停止，不自动重试。

## 历史与协作

[当前交接](docs/next-iteration.md)、[路线图](docs/roadmap.md)、[第六轮产品](docs/iteration-6-product.md)、[历史验证](docs/iteration-6-followup-verification.md)、[品牌规范](docs/brand.md)、[贡献指南](CONTRIBUTING.md)。历史文档描述当时状态，不能替代本轮交付矩阵。没有添加开源许可证。
