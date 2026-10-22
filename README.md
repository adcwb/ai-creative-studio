# 映创 · AI Creative Studio

面向个人与团队的 AI 视觉内容工作室第一阶段原型。React / TypeScript / Vite 前端与 Go / Eino 后端独立启动。

**当前仅提供无凭据、无付费调用的 mock 图像任务。所有结果是程序绘制 SVG，带有“模拟图像 · 非 AI 生成”标识；没有验证任何真实模型质量。** 肖像仅为概念任务分类，不上传或编辑照片。短剧与漫剧工作台尚未实现。

## 目录

- `frontend/`：中文响应式界面、任务轮询、素材库、单元与 Playwright 测试。
- `backend/`：HTTP API、Eino 双节点流程、单进程 worker、持久化和恢复。
- `api/openapi.json`：OpenAPI 3.0.3 契约。
- `docs/architecture.md`：数据归属、扩展边界和生产能力缺口。
- `docs/verification.md`：本次实际验证结果及环境说明。
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

打开 `http://localhost:5173`，API 为 `http://localhost:8080`，健康检查为 `/healthz`。选择“春日品牌海报”，创建模拟任务，待完成后保存到素材库。刷新或重启服务后结果仍可读取。输入 `[mock-fail] 故障测试` 可验证失败路径。

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
```

## 数据和恢复

`backend/data/state.json` 保存任务、SVG 和素材元数据；已加入 Git 忽略。`DATA_DIR` 可覆盖路径。任务先持久化再返回接受响应；执行中任务在重启时重新排队。写文件采用临时文件、文件 fsync、原子 rename，第二个进程无法锁定同一目录。状态损坏时启动失败，**不会清空重建**。停服后备份整个数据目录；要重置演示，请先备份，再显式移走该目录。

当前不支持多实例、高可用、分页、删除、容量管理或整机断电级事务保证。详情见架构文档。未来接入真实 provider 前须完成认证授权、内容安全、成本控制、可观测性和存储迁移。

## GitHub 协作

遵循 [CONTRIBUTING.md](CONTRIBUTING.md)，使用短生命周期分支和可审阅 PR，修改契约时同步实现、文档与测试。第一版已获仓库所有者授权通过普通推送发布至 `master`。仓库公开发布源码，不代表应用已部署或具备生产安全能力；未添加开源许可证。
