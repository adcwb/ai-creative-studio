# 验证记录

执行位置：指定云环境 `/workspace/ai-creative-studio`，2026-10-22。仓库起初为空，实际初始分支为 `work`，已在本地调整为约定的 `master`；没有已有用户文件可被覆盖，未发现仓库说明或 `.agents/skills` 文件。未触碰用户电脑。

以下为首次交付时已实际完成的本地验证记录；后续公开推送已获得用户明确授权，GitHub CI 结果以对应提交的 Actions 运行为准。

## 已通过

| 检查 | 结果 / 覆盖范围 |
| --- | --- |
| `gofmt` 检查 | Go 源文件无待格式化内容 |
| `go vet ./...` | 通过 |
| `go test -race -cover ./...` | 7 个后端测试通过，无数据竞争；internal/studio 语句覆盖率 80.0%；main 包无单元覆盖 |
| `go build -o bin/server ./cmd/server` | 通过，实际用于浏览器和进程恢复测试 |
| `python3 scripts/smoke.py` | 真实服务进程在 running 状态被 SIGKILL 后重启并完成任务；再次重启后 SVG、素材和幂等键保持一致 |
| `npm run lint` | ESLint 零警告通过 |
| `npm run typecheck` | TypeScript 严格类型检查通过 |
| `npm test` | 3 个输入/API 错误处理单元测试通过 |
| `npm run build` | Vite 生产构建通过 |
| `npm run test:e2e` | 系统 Chromium 下桌面/手机 4 项通过；任务创建、图像加载、重复提交、保存素材、刷新保留、空间切换、失败提示、断网恢复；无页面脚本异常和横向溢出 |
| OpenAPI 3.0.3 校验 | `openapi-spec-validator 0.7.2` 通过 |
| Compose 配置解析 | `docker compose --env-file deploy/.env.example -f deploy/compose.yaml --profile future config --quiet` 通过 |
| `git diff --check` | 无报错；新文件仍未暂存，因此本命令不覆盖未跟踪文件内容 |

后端测试还覆盖：Eino 规范化与取消、模拟 provider 失败、SVG 文本转义、跨空间 ID 查询/保存拒绝、同目录并发写者拒绝、损坏状态拒绝启动、并发幂等创建、失败写入不发布内存任务、重复素材保存，以及错误 CORS 来源。

浏览器截图位于 `frontend/test-results/**/studio.png`（忽略、不提交）；已实际查看移动端截图。测试数据保存在 `/tmp` 临时目录，不写入正常开发数据目录。E2E 的临时目录当前不自动清理，环境销毁时可移除。

## 环境问题与解决

- 环境 `/usr/bin/go` 是其他程序，不能运行 `go version`。从官方 Go 下载源获取工具链并验证 SHA-256，写入 `/tmp`；缓存使用 `/tmp/studio-gopath` 和 `/tmp/studio-gocache`。
- 首次获取的 Go 1.27.3 与 Eino 间接依赖 Sonic 1.14.1 不兼容（`undefined: GoMapIterator`）。改用经验证的 Go 1.25.3，已在 `go.mod` 和 CI 中锁定。未宣称已完成依赖安全审计或最新工具链适配。
- 覆盖率首次检查因子进程从 PATH 找到错误的 `/usr/bin/go` 失败；将正确 Go 工具链目录前置 PATH 后完整检查通过。README 已写明此云环境配置。
- Playwright Chromium 下载被网络策略以 HTTP 403 `Domain forbidden` 拒绝。未继续申请网络提升，改用现有 `/usr/bin/chromium`，4 项浏览器测试通过。可通过 `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` 复现。拒绝的是浏览器下载，不是代码实现或测试本身；当前没有尚待授权的阻塞。

## 未运行 / 未验证

- 生成这份本地验证记录时 GitHub 托管 CI 尚未运行；已提供工作流，后续推送会触发检查。
- Compose 只验证配置，未拉取/启动 PostgreSQL、Redis、MinIO；适配器尚未实现。
- 未进行真实 AI 模型调用、图像质量评估、真实肖像编辑、短剧/音视频生成。
- 未验证生产认证、安全隔离、负载/容量、断电恢复、跨平台文件锁、部署或公共访问。真实进程恢复验证不能等同于这些能力。
- 未执行全面依赖漏洞扫描；锁定依赖仅保证可复现，不能证明没有漏洞，生产前须升级审计。

## 首次本地交付时的 Git 状态

`master` 为未产生首个提交的本地分支，所有项目源文件均为新建、未暂存。`origin` 仍是 `https://github.com/adcwb/ai-creative-studio.git`。没有提交、远程分支创建、推送、PR、部署、许可证添加或仓库可见性修改。该记录之后，用户已明确授权将第一版普通推送到公开仓库；不包含部署、可见性修改或许可证变更。

## 公开推送后的首次 GitHub CI

初始提交 `8587f23ca3b1008a8d965d0d120af2141f3493d0` 的 [Checks 运行](https://github.com/adcwb/ai-creative-studio/actions/runs/39743297856) 中，Go 检查、进程恢复和前端检查均通过。浏览器依赖安装失败：`ubuntu-latest` 当时映射为 Ubuntu 26.04，而锁定的 Playwright 1.56.1 不支持该系统。CI 现固定为 Playwright 支持的 `ubuntu-24.04`，保留全部检查及原有只读权限；修复后的结果以对应新提交的 Actions 为准。

首次 CI 的 npm 安装审计报告 12 项漏洞（1 moderate、10 high、1 critical），涉及开发工具依赖。已按审计修复版本将 Vite 升为 7.3.7、Vitest 升为 4.1.12、typescript-eslint 升为 8.72.0，并更新锁文件。升级后本地 `npm audit` 返回 0 项已知漏洞，lint、typecheck、3 项单元测试、生产构建及桌面/移动端 4 项 E2E 均重新通过。此结果只代表当时 npm 审计数据库的检查范围，不是生产安全认证；Go 依赖尚未完成全面漏洞审计。
