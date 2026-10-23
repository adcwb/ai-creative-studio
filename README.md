# 映创 · AI Creative Studio

面向个人与团队的 AI 视觉内容工作室。React / TypeScript / Vite 前端与 Go / Eino 后端独立启动。第四轮已发布并验证。第五轮基于 `b080d6b` 实现可编辑分镜计划，仅本地开发与验证，未推送或部署。

**默认仍为无凭据、无付费调用的 mock 模式。模拟结果是程序绘制 SVG，带有“模拟图像 · 非 AI 生成”标识。第三轮增加一种明确的真实图像协议适配器，但仅以本地 HTTP 假服务验证，未进行真实付费生成或验证任何模型质量。** 肖像仅为概念任务分类，不上传或编辑照片。短剧项目支持结构化模拟剧本与可编辑模拟分镜计划；文本由程序生成并明确标记，未生成分镜图像、音频或视频。

## 第五轮增量

- 从已保存剧本异步生成 `mock-storyboard` 分镜计划；每项目保留一份当前计划。
- 逐镜编辑画面、场景序号、景别、运镜、时长与对白，显式保存到真实后端；刷新与重启后可读取。
- 分镜记录来源剧本版本，剧本编辑或重新生成后提示过期；编辑旧分镜不会消除过期提示。
- 生成失败保留旧计划；生成期间锁定项目写操作，版本冲突保留本地草稿，结果不确定时使用原请求与幂等键重试。
- 仍不提供分镜图像、真实文本模型、镜头增删界面、时间轴或视频编辑器。

契约与数据升级见 [第五轮架构](docs/iteration-5-architecture.md)，实际检查与限制见 [第五轮验证](docs/iteration-5-verification.md)。

## 第四轮增量

- 短剧项目按演示空间创建、浏览和编辑，名称与创作需求真实保存至后端。
- 独立 `mock-script` 经 Eino 校验输入、生成两个角色和三个场景的结构化草稿、校验输出；状态与结果持久化。
- 编辑剧本标题、梗概和场景文本后显式保存；版本检查防止并发覆盖，所有写操作使用持久幂等记录。
- 生成失败保留旧稿；修改创作需求后展示旧稿来源提示，重新生成需明确确认覆盖。
- 不提供真实文本模型、分镜、音视频、成片导出、删除或剧本多版本浏览。

本轮范围及验收见 [产品](docs/iteration-4-product.md)、[架构](docs/iteration-4-architecture.md)、[界面](docs/iteration-4-ui.md)、[验证](docs/iteration-4-verification.md)、[角色协作](docs/iteration-4-collaboration.md)。完整阶段边界见 [路线图](docs/roadmap.md)；Actions Node 24 运行时维护依据见 [CI 说明](docs/iteration-4-ci.md)。

## 第三轮增量

- 修复桌面创作面板底边不齐及长页面侧栏背景提前结束，保留自然内容高度与移动单列布局。
- 增加固定官方 OpenAI Images 协议适配，保留默认 mock；能力接口区分模拟、真实协议配置就绪和配置不可用。配置就绪不代表远端认证或生成已成功。
- 真实 PNG 校验后保存到本地结果目录，任务与素材记录来源、模型及图片元信息；历史 mock 不会因当前模式变化而被标为真实生成。
- 真实请求不自动重试，执行中断不自动重新发送。显式重试可能再次计费，不能承诺上游 exactly-once。

详细范围、协议与最终验证分别见 [产品](docs/iteration-3-product.md)、[架构](docs/iteration-3-architecture.md)、[界面证据](docs/iteration-3-ui.md)、[独立验证](docs/iteration-3-verification.md)和[协作记录](docs/iteration-3-collaboration.md)。

## 第二轮业务增量

- **后端模板目录**：海报、肖像概念、通用图像，比例/风格选项来自 API；所有输入由后端校验，任务与素材持久化参数。
- **可核对的任务详情**：直接读取任务详情 API，展示完整设定、阶段、失败原因及重试来源。
- **真实失败重试**：编辑后通过重试 API 创建关联原任务的新记录并排队执行；原失败记录保留，同一次请求幂等重放，不伪造前端成功。
- **后端素材检索**：关键词与类型组合查询真实持久化素材；查询失败、无匹配和空库分开显示。
- **统一映创品牌**：原创 SVG 标记、深浅背景字标与 favicon，统一设计 tokens、可访问组件及响应式工作台。

第二轮时图像 provider 仅为 mock；业务 API、任务执行、重试关联和持久化均真实运行。三种比例为 `1:1 / 4:5 / 16:9`，三种风格为自然 / 棚拍 / 插画。旧数据缺失参数时补 `4:5 / natural`，保留原 SVG。没有执行中任务取消功能；“取消编辑”和关闭详情不会取消后端任务。

产品、架构、前端、后端、测试五个实际子代理的职责和文件边界见 [协作记录](docs/iteration-2-collaboration.md)。验收范围见 [产品文档](docs/iteration-2-product.md)，接口与兼容设计见 [第二轮架构](docs/iteration-2-architecture.md)，最终验证见 [第二轮测试记录](docs/iteration-2-verification.md)，视觉规则见 [品牌与组件规范](docs/brand.md)。

## 实际运行截图

### 第五轮：可编辑模拟分镜计划

真实本地前后端，使用合成剧本；分镜为程序模拟文本计划，不是分镜图像或视频，也不是已部署服务。

[桌面原图](docs/screenshots/storyboard-desktop.png) · [手机原图](docs/screenshots/storyboard-mobile.png) · [36 状态布局证据](docs/screenshots/iteration-5-storyboard-geometry.json)

![映创当前模拟分镜计划](docs/screenshots/storyboard-desktop.png)

### 第四轮：短剧项目与结构化模拟剧本

以下来自真实运行的前后端，仅使用合成创作需求，剧本文本由程序模拟生成，非真实 AI 创作，也不是视频。图片是静态截图，不是已部署的交互网页。

[桌面原图](docs/screenshots/script-desktop.png) · [手机原图](docs/screenshots/script-mobile.png) · [30 状态布局证据](docs/screenshots/iteration-4-project-geometry.json)

![短剧项目与当前模拟剧本](docs/screenshots/script-desktop.png)

### 图像工作台

以下第四轮图像工作台回归截图来自云开发环境中实际运行的前后端，仅包含合成演示数据。它们是静态截图，**不是可交互网页或已部署服务**；图像结果仍为带标识的模拟 SVG。

### 桌面：创建模拟图像与底边对齐

[打开桌面原图](docs/screenshots/desktop.png)

![桌面工作台：模拟任务完成，两列底边对齐](docs/screenshots/desktop.png)

[修复前桌面截图](docs/screenshots/iteration-3-before-desktop.png) · [本轮 24 个状态的几何证据](docs/screenshots/iteration-4-image-geometry.json)

### 移动端：模板、参数与创作记录

[打开移动端原图](docs/screenshots/mobile.png)

<img src="docs/screenshots/mobile.png" alt="移动端工作台：模板、创作设定与模拟结果" width="390" />

第二轮留档 — 素材检索：[桌面](docs/screenshots/library-desktop.png) · [手机](docs/screenshots/library-mobile.png)

第二轮留档 — 任务详情：[桌面](docs/screenshots/detail-desktop.png) · [手机](docs/screenshots/detail-mobile.png)

## 目录

- `frontend/`：中文响应式界面、任务轮询、素材库、单元与 Playwright 测试。
- `backend/`：HTTP API、图像双节点与剧本三节点 Eino 流程、进程内 workers、持久化和恢复。
- `api/openapi.json`：OpenAPI 3.0.3 契约。
- `docs/architecture.md`：数据归属、扩展边界和生产能力缺口。
- `docs/iteration-2-verification.md`：第二轮实际验证结果；`docs/verification.md` 保留第一轮记录。
- `deploy/`：未来 PostgreSQL、Redis、MinIO 本地设施示例，当前应用不连接这些设施。
- `.github/`：CI、问题和 PR 模板。项目未添加开源许可证。

## 本地开发

先决条件：Linux（文件存储使用 `flock`；macOS 可自行验证，Windows 建议 WSL）、Go **1.25.3**、Node.js **24**、npm **11**。Go 与 Eino 版本锁定以确保兼容；升级前须复跑测试。

终端一：

```bash
cd backend
cp .env.example .env
set -a; . ./.env; set +a
# .env 为可信本地配置，服务本身不自动读取 .env
go mod download
go run ./cmd/server
```

终端二：

```bash
cd frontend
cp .env.example .env
npm ci
npm run dev
```

打开 `http://localhost:5173`，API 为 `http://localhost:8080`，健康检查为 `/healthz`。从后端模板中选择一个起点，调整描述、比例与风格，创建模拟任务后查看详情并保存素材；在素材库按关键词和类型搜索。刷新或重启服务后已提交记录仍可读取。输入 `[mock-fail] 故障测试` 可验证失败路径；点击编辑重试并手动移除标记，可观察新任务执行与原记录保留。未提交的草稿不是后端记录。

服务默认仅绑定回环地址。**没有认证，任何能访问服务的人都能读写两个演示空间。不要暴露到公网或输入真实客户数据。** `space_id` 只演示数据归属过滤，不是安全边界。需要在云工作区访问界面时，通过平台已有端口转发访问；本项目不自动部署或创建公开预览。浏览器中的 `VITE_API_BASE_URL` 必须是浏览器可达的 API 地址，`FRONTEND_ORIGIN` 必须精确匹配前端来源。

本次云环境中 `/usr/bin/go` 不是 Go 编译器，可在后端命令前执行以下环境配置（工具链在临时目录，环境重建后需重新安装）：

```bash
export PATH=/tmp/studio-go125/go/bin:$PATH
export GOPATH=/tmp/studio-gopath
export GOCACHE=/tmp/studio-gocache
```

## 服务模式与安全配置

默认 `IMAGE_PROVIDER=mock-svg`，无需模型密钥。`GET /api/v1/capabilities` 返回模式和本地配置状态，前端据此展示生成入口；缺少密钥或配置无效时仍可查看历史记录，但不能创建新任务。已有请求的幂等重放保留原任务。

后续若要实际调用，需由操作者在后端受保护的运行环境配置 `IMAGE_PROVIDER=openai-images` 和 `OPENAI_API_KEY`，并自行确认供应商账号权限、预算和数据发送授权。不要在聊天、前端变量、源码或 Git 中填写密钥。示例配置不包含真实密钥；本轮没有执行真实生成。

该适配器只支持固定官方 `https://api.openai.com/v1/images/generations` 和模型 `gpt-image-2-2026-04-21`，不是通用兼容网关。请求为单张、low质量、PNG，比例精确映射为1024×1024、1024×1280、1536×864；类型和风格写入提示词。它不接受任意 base URL、不跟随重定向、不使用环境代理、不下载供应商图片 URL。

真实请求最多等待120秒，不自动重试。超时、中断或响应无效并不证明供应商未计费；执行中的真实任务重启后标记失败并保留不确定性，显式重试可能再次计费。默认 mock 则继续原有安全重放恢复方式。模式切换不会改写历史素材来源。

这仍是无生产认证的本地演示服务；配置真实 provider 不会补齐鉴权、用量限制或企业隔离，不能据此直接暴露到公网。

## 检查

```bash
cd backend
test -z "$(gofmt -l cmd internal)"
go vet ./...
go test -race ./...
go build -o bin/server ./cmd/server
cd ..
python3 scripts/smoke.py
cd frontend
npm ci
npm run lint
npm run typecheck
npm test
npm run build
npx playwright install chromium
npm run test:e2e
```

E2E 自动启动独立 API（8081）与前端（5174），使用新建临时数据目录，不读取开发数据。若已有系统 Chromium，可跳过下载：

```bash
PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/usr/bin/chromium npm run test:e2e
```

布局几何回归（从仓库根目录执行，自动使用隔离数据与随机端口）：

```bash
node scripts/layout_check.mjs
node scripts/provider_check.mjs
node scripts/project_check.mjs
node scripts/storyboard_check.mjs
```

系统 Chromium 可通过同一个 `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` 指定。该检查覆盖多个桌面宽度、短屏、单列断点、手机、空库及长文本，不使用开发数据。

Provider 联调使用本地 HTTP 假服务及合成 PNG，验证 Eino、落盘、重启、显式重试和过期模式保护，不调用外部模型或使用真实凭据。

OpenAPI 检查（建议在独立 Python 虚拟环境执行）：

```bash
pip install openapi-spec-validator==0.7.2
python -m openapi_spec_validator api/openapi.json
python scripts/contract_check.py
```

契约回归会编译当前后端，以临时数据目录和随机回环端口验证真实 HTTP 响应、重试执行、素材筛选与重启后幂等，然后清理进程和临时数据。

## 数据和恢复

`backend/data/state.json` 保存图像任务、历史模拟 SVG、素材元数据，以及短剧项目、当前剧本和分镜计划、生成记录和写操作幂等记录；真实 PNG 保存在同目录下的 `results/`；整个运行数据目录已加入 Git 忽略。`DATA_DIR` 可覆盖路径。任务先持久化再返回接受响应；mock 执行中任务在重启时重新排队，真实执行中任务转为结果不确定的失败，避免自动重复调用。写文件采用临时文件、文件 fsync、原子 rename，第二个进程无法锁定同一目录。状态损坏时启动失败，**不会清空重建**。停服后备份整个数据目录；要重置演示，请先备份，再显式移走该目录。

第五轮使用 v3 文件快照，支持完整 v1/v2 数据受控升级，保留已有图像、项目、剧本与回执。升级前停服备份整个 `DATA_DIR`；旧版本程序不能直接读取 v3，回退须恢复对应备份，不要手改版本号。损坏或缺失必需字段仍拒绝启动。此升级不涉及数据库或外部服务。

短剧 `running` 在重启后重新排队，仅用于无外部调用的 mock；图像真实模式仍遵守中断不自动重发规则。

当前不支持多实例、高可用、分页、删除、容量管理或整机断电级事务保证。详情见架构文档。在真实 provider 用于实际用户前，须完成认证授权、内容安全、成本控制、可观测性和生产存储迁移。本轮只验证适配协议与业务流程，不补齐这些生产能力。

## GitHub 协作

遵循 [CONTRIBUTING.md](CONTRIBUTING.md)，使用短生命周期分支和可审阅 PR，修改契约时同步实现、文档与测试。第一版已获仓库所有者授权通过普通推送发布至 `master`。仓库公开发布源码，不代表应用已部署或具备生产安全能力；未添加开源许可证。
