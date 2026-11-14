# 映创 · AI Creative Studio

**简体中文** · [English](README.en.md)

从创意、剧本和分镜，到图片、配音、字幕、海报与短片的创作工作室。React / TypeScript 前端与 Go / Eino 后端分离；支持本地文件模式，也提供 PostgreSQL、独立 worker 和对象存储适配。

界面目前完整支持**简体中文和英文**，可以切换并记住语言，首次访问按浏览器语言回退。语言与币种独立：默认 USD，账本和预算按各币种整数最小单位分别处理，不静默兑换。翻译结构可扩展，不表示已经支持其他语言。

登录页提供中/en直接切换、三幅原创高清概念图轮播与暂停；未来交互型数字人明确标注规划中。登录后的“系统设置”可选择森林工作室、暖纸画廊、深色放映室，语言与主题按账户保存在当前浏览器，退出与切换账户不会沿用其他用户偏好；目前不提供跨设备同步。[设计与资源记录](docs/design/2026-10-31-appearance.md)


登录画廊默认自动播放、无页码，尊重系统减弱动画；侧栏及一级菜单可收折，所有工作区共享上下文页眉。登录后语言仅在系统设置中选择；管理员可在同页维护组织模型网关。旧供应商编辑入口退役，历史数据不删除。[配置归属与迁移](docs/settings-configuration.md) · [新版设计](docs/design/2026-10-31-navigation-gateway.md)

## 可以做什么

| 工作流 | 能力 |
| --- | --- |
| 创意到分镜 | 项目简报、剧本、镜头计划、角色参考、版本历史及素材管理 |
| 图片与海报 | 图像生成适配、参考图与 mask 编辑；海报文字、Logo 图层的保存与 PNG 导出 |
| 短片编辑 | 镜头裁剪、排序、拆分与复制；多条定位音轨、图片／视频叠层、位置与透明度关键帧 |
| 预览与输出 | 浏览器画面预览和时间轴拖动；可组合的镜头转场；H.264/AAC MP4、横屏／竖屏／方形、360p 至有界 4K |
| 声音与字幕 | 显式原声混音、背景音乐、人工字幕、ASR 候选、字幕烧录与 SRT/VTT；纯文本转写与带时间戳字幕分开 |
| 发布准备 | 小红书、抖音、TikTok 发布中心；按平台审批、授权及协议区分导出、App 交接、草稿与直发 |
| 团队与治理 | 登录、组织、服务端角色权限、租户隔离、模型配置、个人/团队空间、RBAC、可选审核与脱敏审计（项目不设费用或额度门槛） |

以上功能的具体实现、测试结果及未完成部分以[本轮交付矩阵](docs/iteration-10-delivery.md)为准。合成素材、模拟上游和离线协议测试不代表真实模型效果或社交平台审批通过。

## 编辑能力与限制

这是一套渐进扩展的短片编辑器，不是完整专业NLE。视频输出24fps，可选择1K·1080p、2K·1440p、3K·1800p和4K·2160p，保留360p/720p预览档。已移除分辨率与总时长耦合及旧镜头/音轨/叠层条数限制，导出和对象传输支持流式文件；素材原生画质与后期放大明确区分。完整60/90/120秒合成测试及当前未完成部分见[实施记录](docs/superpowers/specs/2026-11-04-creative-series/implementation.md)。每层仍以有界关键帧和内置表达式处理；不承诺HDR、无损母版、实时4K、模型原生4K或200集生产已就绪。

浏览器预览用于画面编排，是低分辨率无声预览；最终字幕、声音和压缩结果以实际导出为准。转场使用允许的内置效果，不接受任意 FFmpeg 表达式。交叉溶解、擦除、滑动和圆形展开会重叠相邻镜头，字幕与音轨使用重叠后的时间轴。详见[媒体规格](docs/iteration-10-media.md)。

海报保存可编辑图层，导出新 PNG 素材；已有图片不会被覆盖。角色参考和一致性反馈不保证锁脸；强制对齐、口型及音效的[具体离线引擎](docs/local-model-engines.md)尚未完成真实权重质量验收，部分上游模型限非商用。

## 社交平台：配置与发布是两回事

发布中心不会把素材下载、转到 App、上传草稿或提交审核显示成“已发布”。TikTok 支持视频与图片直发／草稿协议，需要应用审批、用户授权，图片还需平台已验证的公开素材域名；抖音普通创作工具走批准的 H5 分享，支持官方 ticket 获取、缓存和到期刷新；企业代发有额外账号与权限条件；小红书公开分享平台接入受限，没有已核实的通用服务器笔记直发接口。

默认未配置时只提供真实素材导出；小红书支持用户主动复制本地文案，不自动填入平台。PNG海报可经明确确认转换为新的JPEG，再预览、排序及选择封面；原图不变。图片发送另需确认最长65分钟的受控公开读取。TikTok在显式操作中自动续期令牌，轮换结果加密保存，未知刷新结果要求重新授权，不重发帖子。[图片合同](docs/social-photo.md) · [令牌续期](docs/social-token-refresh.md)

账户连接、内容发送和发布结果查询必须经过对应平台的服务端授权与能力检查；创建结果未知时保留回执，不盲目重发。本项目本轮只使用合成账号和假上游测试，**没有执行真实 OAuth、上传或对外发布**。[协议与状态边界](docs/iteration-10-social.md)

## 模型生态

项目内现已提供[数字人口播入口](docs/digital-human.md)：形象授权登记与撤销、TTS／录音选择、照片驱动与视频改口型任务、带 AI 标识的成片入库。默认未启用人物模型，需安装具备适当许可的执行器并完成真实样片验收。

网页3D入口现提供[TalkingHead + Three.js角色与配音预览](docs/superpowers/specs/2026-11-13-workflow-canvas-digital-human/web-3d.md)：CC0示例或已授权私有GLB、已有网关文案/配音、可撤销使用声明与浏览器基础音量口型。不是中文音素对齐、照片转3D或3D成片导出；真实移动GPU、供应商效果和商业账户权益未验收。


提供 OpenAI、Anthropic / Claude、Google / Gemini / Nano Banana、字节 Ark / 豆包、MiniMax 和即梦公开视觉 API 的可替换适配。不同厂商和型号的文本、图像、视频、语音与转写能力不同，配置型号不等于协议兼容或账户获权。

OpenAI Videos / Sora 已退役并在本地拒绝；即梦视频没有已核实的可用新版官方接口。旧语音与转写接口生命周期见[候选合同核对](docs/iteration-9-followup.md)。真实账号、效果、耗时和费用尚未验证，不以 mock 结果代替。[真实验收准备](docs/live-evaluation.md)默认 dry-run，独立评测工具仍要求明确授权。创作页面已移除报价、费用确认和预算限制；配置启用模型后，在组织授权与能力匹配时直接生成；本项目自动审核默认暂时禁用，结果未经自动审核，详见[执行与兼容说明](docs/runtime-model-selection.md)。

## 本地启动

依赖：Go 1.25.3、Node.js 24 / npm 11、FFmpeg / FFprobe 和中文字体。后端只读启动环境，不自动加载 `.env`。示例仅供本机 HTTP、全新测试账号：

```bash
# 终端 1
cd backend
AUTH_ALLOW_REGISTRATION=true AUTH_COOKIE_SECURE=false go run ./cmd/server
```

```bash
# 终端 2
cd frontend
npm ci
npm run dev
```

打开 `http://localhost:5173`，后端监听 `http://localhost:8080`。默认注册关闭、Secure cookie 开启；公网部署需 TLS，不能照搬本机 cookie 开关。若显式使用 `AUTH_MODE=demo`，空间只是公开演示过滤，不是租户安全边界，不得放真实客户数据。[身份说明](docs/authentication.md)

## Docker 与持久化部署

```bash
docker compose build
docker compose up -d
```

前端通过 Nginx 同源代理后端，默认只绑定回环。按[Docker 配置](docs/docker.md)配置注册、TLS、持久卷与资源限制。镜像发布不表示应用已上线；本轮开发不自动推送、部署或创建云资源。

文件模式必须备份整个 `DATA_DIR`。PostgreSQL 模式使用数据库事务与队列、独立 worker 和共享 BlobStore；迁移前停写并使用空目标，不能只切换环境变量。[数据库与 worker](docs/postgres-workers.md) · [对象存储](docs/iteration-8-object-storage.md) · [迁移和备份](docs/postgres-backup.md)

部署密钥由部署方注入，没有默认值；列表和审计不回显供应商密钥。加密备份不包含 KEK，需独立保管；新增图层和发布状态不能由旧版本程序无损读取。回滚应恢复匹配版本完整备份。[备份加密](docs/backup-encryption-retention.md) · [密钥轮换](docs/model-key-rotation.md)

## 开发与验收

```bash
cd backend
go vet ./...
# 三个互补分组都须运行，保留全部测试覆盖，避免大媒体与数据库回归共用单包10分钟计时。
go test -race ./... -run '^Test[A-M]'
go test -race ./... -skip '^(TestNative|Test[A-M])'
go test -race ./... -run '^TestNative'
go build ./cmd/server
cd ../frontend
npm ci
npm run lint
npm run typecheck
npm test
npm run build
PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/usr/bin/chromium npm run test:e2e
```

完整验收还包括 OpenAPI、跨租户安全、恢复、实际媒体、桌面／手机布局和多服务测试；入口见[本轮验证](docs/iteration-10-verification.md)及 `.github/workflows/ci.yml`。测试数据均为合成素材或临时账号，不应提交凭据、私人素材或生成媒体。

[当前交接](docs/next-iteration.md) · [路线图](docs/roadmap.md) · [贡献指南](CONTRIBUTING.md) · [国际化](docs/internationalization.md)

[中英文桌面／手机合成截图](docs/screenshots/iteration-10/README.md)（仅测试数据，不代表真实平台发布）。

### 自定义图像提示词（本地开发阶段）

图像页可将当前描述或成功任务保存为私人提示词，管理分类、标签、收藏和历史版本，并明确共享至当前组织。选用只预填；点击创建后仍走当前组织绑定的真实网关。保存、预览和选用不调用模型。修改保留新版本，冲突保留草稿，未确认保存可跨刷新恢复原请求。支持纯文本与明确声明的文字/枚举变量；纯文本中的花括号仍按字面保存。变量配方须先通过服务端完整预览后填入创作区，任务固定变量和版本。支持固定版本、有序参考图配方，在项目制作中明确应用后再生成；模型必须支持实际参考组合，不能静默丢图。现有系列/季/集和独立项目已接入，批量制作与200集容量验收仍在后续阶段。详见[提示词记录](docs/superpowers/specs/2026-11-04-creative-series/prompts-implementation.md)与[参考配方记录](docs/superpowers/specs/2026-11-04-creative-series/references-implementation.md)。

本地后续修复将继续编辑原图、照片/蒙版保存改为现有分块上传，二进制网关音视频及原生视频下载、审核与入库走文件流式链路，移除这些路径的旧32MiB及蒙版4MiB门槛；保留实际像素、格式、权限及显式required模式的审核检查。准确支持范围及其他残余限制见[媒体流式实施记录](docs/superpowers/specs/2026-11-04-creative-series/media-streaming-implementation.md)。这不是已经发布的新镜像。
原生JSON/hex和多参考路径继续采用有界流式编码/解码，移除聚合10MiB等项目字节门槛；Google内联容量由实际模型上游决定，MiniMax V2视频30MB单图/64MB请求按已核验协议处理，不是项目额度。其他适配器、像素及素材时长支持边界详见同一实施记录；当前仍仅本地验证，不表示远端CI或镜像已成功。
本地核心收尾已将通用网关图像JSON/URL接为流式，取消素材180秒及网关ASR/视觉输入的旧字节/时长门槛，前端按实际素材与时间轴显示范围；供应商原生协议、格式安全和旧插件支持范围仍须区分，详见同一实施记录。尚未推送或验证本轮云端发布。

镜头制作与剪辑页现支持可暂停、刷新后重选原文件恢复的分块上传；先校验完整文件，再登记素材，无项目累计容量配额。当前为本地开发阶段，S3原生multipart恢复已实现，详见[S3实施记录](docs/superpowers/specs/2026-11-04-creative-series/s3-implementation.md)；新版本导出保存分段进度，服务重启后继续，失败或取消后可明确继续原时间轴，详见[渲染记录](docs/superpowers/specs/2026-11-04-creative-series/render-implementation.md)；上传交互详见[上传实施记录](docs/superpowers/specs/2026-11-04-creative-series/uploads-implementation.md)。

网关安全模式浏览器回归使用 `npm --prefix frontend run test:gateway`。桌面和手机分别启动独立临时后端，合成账号不会共用注册防滥用窗口；生产认证限流保持不变。单个设备可用 Playwright 的 `--project=desktop` 或 `--project=mobile` 单独排查。
