# 映创 · AI Creative Studio

面向个人与团队的 AI 视觉内容工作室。React / TypeScript / Vite 前端与 Go / Eino 后端独立启动。当前云工作区为第二轮本地迭代，尚未获准推送或部署；已发布第一版基线为 `7284fab`。

**当前仅提供无凭据、无付费调用的 mock 图像任务。所有结果是程序绘制 SVG，带有“模拟图像 · 非 AI 生成”标识；没有验证任何真实模型质量。** 肖像仅为概念任务分类，不上传或编辑照片。短剧与漫剧工作台尚未实现。

## 第二轮业务增量

- **后端模板目录**：海报、肖像概念、通用图像，比例/风格选项来自 API；所有输入由后端校验，任务与素材持久化参数。
- **可核对的任务详情**：直接读取任务详情 API，展示完整设定、阶段、失败原因及重试来源。
- **真实失败重试**：编辑后通过重试 API 创建关联原任务的新记录并排队执行；原失败记录保留，同一次请求幂等重放，不伪造前端成功。
- **后端素材检索**：关键词与类型组合查询真实持久化素材；查询失败、无匹配和空库分开显示。
- **统一映创品牌**：原创 SVG 标记、深浅背景字标与 favicon，统一设计 tokens、可访问组件及响应式工作台。

图像 provider 仍是 mock；业务 API、任务执行、重试关联和持久化均真实运行。三种比例为 `1:1 / 4:5 / 16:9`，三种风格为自然 / 棚拍 / 插画。旧数据缺失参数时补 `4:5 / natural`，保留原 SVG。没有执行中任务取消功能；“取消编辑”和关闭详情不会取消后端任务。

产品、架构、前端、后端、测试五个实际子代理的职责和文件边界见 [协作记录](docs/iteration-2-collaboration.md)。验收范围见 [产品文档](docs/iteration-2-product.md)，接口与兼容设计见 [第二轮架构](docs/iteration-2-architecture.md)，最终验证见 [第二轮测试记录](docs/iteration-2-verification.md)，视觉规则见 [品牌与组件规范](docs/brand.md)。

## 实际运行截图

以下第二轮截图来自云开发环境中实际运行的前后端，仅包含合成演示数据。它们是静态截图，**不是可交互网页或已部署服务**；图像结果仍为带标识的模拟 SVG。本轮截图与代码尚未推送。

### 桌面：创建模拟图像并保存素材

[打开桌面原图](docs/screenshots/desktop.png)

![桌面工作台：模拟任务完成并保存到素材库](docs/screenshots/desktop.png)

### 移动端：模板、参数与创作记录

[打开移动端原图](docs/screenshots/mobile.png)

<img src="docs/screenshots/mobile.png" alt="移动端工作台：模板、创作设定与模拟结果" width="390" />

素材检索：[桌面](docs/screenshots/library-desktop.png) · [手机](docs/screenshots/library-mobile.png)

任务详情：[桌面](docs/screenshots/detail-desktop.png) · [手机](docs/screenshots/detail-mobile.png)

## 目录

- `frontend/`：中文响应式界面、任务轮询、素材库、单元与 Playwright 测试。
- `backend/`：HTTP API、Eino 双节点流程、单进程 worker、持久化和恢复。
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

OpenAPI 检查（建议在独立 Python 虚拟环境执行）：

```bash
pip install openapi-spec-validator==0.7.2
python -m openapi_spec_validator api/openapi.json
python scripts/contract_check.py
```

契约回归会编译当前后端，以临时数据目录和随机回环端口验证真实 HTTP 响应、重试执行、素材筛选与重启后幂等，然后清理进程和临时数据。

## 数据和恢复

`backend/data/state.json` 保存任务、SVG 和素材元数据；已加入 Git 忽略。`DATA_DIR` 可覆盖路径。任务先持久化再返回接受响应；执行中任务在重启时重新排队。写文件采用临时文件、文件 fsync、原子 rename，第二个进程无法锁定同一目录。状态损坏时启动失败，**不会清空重建**。停服后备份整个数据目录；要重置演示，请先备份，再显式移走该目录。

当前不支持多实例、高可用、分页、删除、容量管理或整机断电级事务保证。详情见架构文档。未来接入真实 provider 前须完成认证授权、内容安全、成本控制、可观测性和存储迁移。

## GitHub 协作

遵循 [CONTRIBUTING.md](CONTRIBUTING.md)，使用短生命周期分支和可审阅 PR，修改契约时同步实现、文档与测试。第一版已获仓库所有者授权通过普通推送发布至 `master`。仓库公开发布源码，不代表应用已部署或具备生产安全能力；未添加开源许可证。
