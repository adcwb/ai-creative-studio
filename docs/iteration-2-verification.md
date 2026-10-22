# 第二轮独立验证记录

验证环境：指定云工作区 `/workspace/ai-creative-studio`，日期 2026-10-22，基线 `7284fab`。测试人员仅修改测试、进程 smoke 和本记录；业务修复反馈各文件所有者处理。没有推送、创建 PR、部署或连接付费模型。

## 已完成的检查

| 检查 | 实际结果 |
| --- | --- |
| `go test -race -cover ./...` | 16 项后端测试通过，无数据竞争；`internal/studio` 语句覆盖率 86.9%，`cmd/server` 无单元覆盖 |
| `go vet ./...` | 通过 |
| `go build -o bin/server ./cmd/server` | 通过，产物用于实际服务进程测试 |
| `python3 scripts/smoke.py` | 真实 HTTP 服务完成模板读取、失败父任务重试、子任务 running 期间 SIGKILL、重启继续执行、第二次重启后父子关系/素材/参数/SVG/幂等保留、真实服务端组合筛选及空间过滤 |
| `gofmt` 检查 | `cmd` 与 `internal` 无待格式化内容 |
| `npm run lint` | 最终集成代码通过，零警告 |
| `npm run typecheck` | 最终集成代码严格类型检查通过 |
| `npm test` | 4 项 API/输入单元测试通过 |
| `npm run build` | TypeScript + Vite 生产构建通过 |
| `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/usr/bin/chromium npm run test:e2e` | 最终冻结代码桌面/手机共 16 项全部通过，耗时约 1.3 分钟；无页面脚本错误，已检查页面无横向溢出 |
| OpenAPI 3.0.3 结构校验 | `PYTHONPATH=/tmp/studio-openapi-validator python3 -m openapi_spec_validator api/openapi.json` 通过 |
| 真实 HTTP 契约检查 | 架构和主集成执行 `scripts/contract_check.py`，60 个实际响应（含轮询）符合 OpenAPI 响应 schema；覆盖重试、重启、参数、素材查询及错误响应 |
| 依赖文件核对 | `frontend/package.json` 和 `frontend/package-lock.json` 对比 `7284fab` 无变更；主集成另外运行的 npm audit 返回 0 项，原始结果位于 `/tmp/studio-round2-audit.json` |
| `git diff --check` | 通过 |

Go 命令使用 `PATH=/tmp/studio-go125/go/bin:$PATH GOPATH=/tmp/studio-gopath GOCACHE=/tmp/studio-gocache`。默认 Python 环境没有 OpenAPI 校验包，使用已有的 `/tmp/studio-openapi-validator` 目标目录完成校验，没有修改项目依赖。

真实 HTTP 契约检查可复现命令：`PYTHONPATH=/tmp/studio-openapi-validator PATH=/tmp/studio-go125/go/bin:$PATH GOPATH=/tmp/studio-gopath GOCACHE=/tmp/studio-gocache python3 scripts/contract_check.py`。脚本使用随机本地端口与临时目录，不复用开发数据。

## 后端验收范围

- 新旧请求兼容：模板类型 3 种、比例 3 种、风格 3 种合法组合；Unicode 描述边界；省略、空字符串与显式默认值经过归一化后幂等一致。普通创建和重试均拒绝非法枚举、数字、对象、数组、显式 `null`，且不产生任务。
- Eino 实际调用：记录 provider 收到的归一化参数和返回值；非法输入在 provider 调用之前失败。解析 27 种组合的真实 SVG 验证画布比例、viewBox、脚本文本转义和模拟标记；排除文本与 `data-*` 元数据后比较图形元素，确认类型/风格改变实际图形。
- 存储兼容：从手写旧版 version 1 快照读取缺失参数，保留任务 ID、创建时间、幂等键、历史 SVG、素材 ID；running 恢复 queued；二次重启后保留迁移结果。
- 重试业务：仅失败父任务可重试；缺失/跨空间父任务返回 404；成功/queued 父任务返回 409；同键同父同参数返回原子任务，改参数、跨父、普通创建与重试混用键返回 409；12 个并发请求只创建一个子任务；原失败记录不变，子任务由真实 worker/Eino 执行。重启后 `retry_of`、结果和素材参数持久化。
- 素材查询：API 从已保存素材中按描述与类型组合筛选，大小写无关、关键词去空白、Unicode 上限 100；空结果与非法查询有对应返回；跨空间不返回素材。模板与参数选项由后端端点提供，并验证每个模板可通过后端输入校验。

首轮独立测试发现后端模板默认值与冻结合同不一致；反馈后端所有者修复后通过。测试没有放宽冻结合同来掩盖实现差异。

## 浏览器验证

系统 Chromium 下，桌面与 iPhone 13 尺寸各执行以下 8 条路径，全部通过：

1. 后端模板目录驱动输入及参数；逐一选用 3 个模板；双击创建与后续重复提交只产生一个真实任务，后续响应为 200 且 ID 相同；SVG 图片实际加载比例为 16:9。
2. 查看详情实际调用单任务 API；Tab/Shift+Tab 焦点在弹窗内循环、Escape/关闭按钮关闭且焦点回到触发按钮；取消编辑恢复原草稿的描述、类型、比例和风格且不发 POST；刷新放弃未提交编辑，原失败任务仍在。
3. 失败任务通过真实重试 API 创建子任务后故意丢弃首个响应；客户端保持编辑与原幂等键，重复提交返回唯一子任务；原失败任务保留，子任务实际执行成功；确认成功后退出编辑；保存与刷新后关联仍可在详情读取。
4. 已保存素材的搜索与类型筛选发出真实 `q`/`kind` 请求，响应 IDs 与画面一致；延迟旧类型请求的实际响应，切换新类型查询完成后再释放旧响应，结果不被覆盖；零匹配、清除条件、查询失败后重试与切换空间均验证。
5. 普通创建已被后端接受但响应仍挂起时切换空间，迟到响应不污染新空间；切回与刷新后可读取原空间的持久任务并确认完成。
6. 空间列表、模板目录、任务详情各自网络失败可见且可恢复；保存失败后按钮恢复，直接查询后端确认未保存；恢复后实际保存成功且刷新可检索。
7. 原有完整创建、图片加载、重复提交、保存素材、刷新保留、空间切换及失败提示路径继续通过。
8. 任务列表连接失败可见，重新连接恢复。

首轮为 13/16 通过：详情弹窗在桌面/手机的 Tab 边界会将焦点移出弹窗，反馈前端所有者加入显式闭环后修复；桌面素材用例恰逢空间列表实现热更新，页面重置导致等待搜索响应超时。代码冻结后完整重跑 16/16 通过，没有放宽验收断言。首轮 trace 保存在 `/tmp/studio-round2-e2e-first-run`，最终普通测试截图在忽略目录 `frontend/test-results`。

另以全新临时 `DATA_DIR`、随机 API/前端端口和后端提供的中文合成模板生成 6 张展示截图，逐张实际查看。图片来自真实浏览器和实际保存的模拟 SVG；拍摄前清除元素焦点并回到页面顶部，无后期图像修改。桌面为 1440px 宽，手机使用 iPhone 13 模拟尺寸；手机详情截图是可纵向滚动弹窗的当前视口，没有截全下方全部字段。专用服务进程已停止、临时数据已清理。

| 画面 | 桌面 | 手机 |
| --- | --- | --- |
| 工作台 | [桌面工作台](screenshots/desktop.png) | [手机工作台](screenshots/mobile.png) |
| 素材库 | [桌面素材库](screenshots/library-desktop.png) | [手机素材库](screenshots/library-mobile.png) |
| 任务详情 | [桌面详情](screenshots/detail-desktop.png) | [手机详情](screenshots/detail-mobile.png) |

## 证据边界

AI 图像 provider 仍是程序绘制的模拟 SVG；以上验证只证明真实业务 API、校验、持久化、调度与错误路径，不证明真实 AI 图像生成质量。未验证生产认证、企业级权限隔离、容量负载、断电恢复、外部模型或生产部署。第二轮未触发远程 CI；本地通过不等于远程已运行。
