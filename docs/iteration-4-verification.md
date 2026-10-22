# 第四轮独立验证记录

基线 `7dab4ea`，2026-10-22，指定工作区 `/workspace/ai-creative-studio`。全部使用临时测试数据、本地服务与合成内容；没有真实凭据、付费模型调用、推送或部署。

## 最终验证表

| 检查 | 实际结果 |
| --- | --- |
| Go `test -race -count=1 -timeout=60s ./...` | QA 通过，40 项常规顶层测试（原 28 + 新 12），studio 15.312s，无数据竞争 |
| Go vet / build / gofmt | 最终通过；生产二进制用于浏览器和进程测试 |
| 前端 lint / typecheck / unit / build | QA 全部通过；3 文件 10 项单元测试 |
| 桌面/手机 E2E | 最终 28/28 全部通过，2.0 分钟：原有 16 项及新增 12 项 |
| `scripts/smoke.py` | QA 通过：原图像链保留；新增项目、剧本生成中 SIGKILL、重启恢复、人工保存/版本/receipt、失败保稿与图像能力不可用时独立生成 |
| `scripts/project_check.mjs` | QA 30 状态通过：6 视口 × 空项目/生成结果/最长内容/编辑/失败；真实后端图像缺密钥配置下模拟剧本仍可完成 |
| `scripts/layout_check.mjs` | QA 原有图像 24 状态全部通过，保留全部几何断言及双动画帧焦点等待 |
| `scripts/provider_check.mjs` | QA 原有 PNG/协议/中断回归通过；5 次明确本地假上游调用，幂等重放/守卫/中断恢复未产生额外调用 |
| OpenAPI 结构与真实响应 | 架构最终标准校验通过，20 schemas / 62 examples / 101 refs；仓库 `scripts/contract_check.py` 255 个实际 HTTP 响应通过，包含轮询，不能视作 255 个独立用例 |
| 依赖与静态检查 | 主集成 npm audit 0 项；应用 package/lock、go.mod/go.sum 未改；文档链接/忽略路径扫描通过；QA `git diff --check` 通过 |
| Actions 配置维护 | 主集成完成 Node 24 Action SHA 固定与本地 YAML/配置检查，见 [CI 记录](iteration-4-ci.md)；本轮未推送，更新后的远端 workflow 未运行 |

`TestProviderHarness` 在常规 Go test 中有意跳过；`provider_check.mjs` 显式构建并运行该测试二进制、注入 literal loopback 假上游，实际执行 Handler、worker、Eino 与文件存储。生产二进制没有测试 endpoint 开关。

本机 Go 命令前缀：`PATH=/tmp/studio-go125/go/bin:$PATH GOPATH=/tmp/studio-gopath GOCACHE=/tmp/studio-gocache`。浏览器使用 `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/usr/bin/chromium`；不设置该项时使用 Playwright 已安装的 Chromium。Python 契约检查另加 `PYTHONPATH=/tmp/studio-openapi-validator`。测试脚本显式选择 mock，或缺密钥的不可用图像配置，不会继承宿主真实图像密钥。`project_check.mjs` 与原有两个 Node 脚本均使用随机端口和临时目录，无新增依赖。

## 新增后端证据

12 项新 Go 测试位于 `project_http_test.go`、`project_test.go`、`project_recovery_test.go`：

- 项目、版本和完整剧本校验涵盖 trim 后 Unicode 边界、缺失、类型/未知字段、任意层 null、重复字段 null、64 KiB 请求限制；拒绝后项目与版本不变。角色必须恰为 2 个且名称唯一，场景恰为 3 个，对白 1–6 条并引用已有角色。
- Eino 实际调用收到归一化需求，校验 provider 返回结构；非法输入不进入 provider，模拟失败与取消不会伪成功。
- receipt 重放先于 active/version 检查；项目写重放返回当前投影，不覆盖后续新版本；生成重放返回原尝试当前状态。跨项目/操作复用键冲突，跨空间键独立，项目域与图像域独立。
- 12 个并发同版本更新仅一次成功；12 个同键生成请求仅创建一个尝试。生成期间写操作被拒绝；成功仅增一版，失败保留旧稿、来源 brief 和内容版本。
- 修改 Get/List 返回的角色、场景、对白不会污染存储；保存提交失败不改变内存嵌套内容、原文件或消耗幂等键。
- 完整 v1 快照升级 v2 保留原 SVG/PNG 字节、素材、provider/model、ID 与图像幂等键；v2 任一 required map 缺失或 null 拒启且原文件不改。生成 running 重启 queued，生成和人工保存的 receipt 跨重启有效。

进程 smoke 另外在生成已 running 时实际杀死服务器；重启后同一次生成完成，记录只有一条且版本只增一次。人工保存后再次重启，旧键重放不再增版；后续模拟生成失败仍保留人工稿。最后以 `openai-images` 且空密钥重启，图像能力返回未就绪，独立 `mock-script` 仍成功生成。

## 浏览器与布局证据

新增 6 条路径在桌面/手机各运行一次：

1. 真实创建项目、异步生成、重复点击唯一；取消剧本编辑不写后端；保存已接受但响应丢失后保持草稿，以原 body/key 重试，刷新读取已保存版本。
2. 另一客户端先更新版本后，陈旧剧本保存返回 409，保留本地草稿；确认弹窗支持 Tab/ Escape，只有明确放弃后才载入服务端新版本。
3. 未保存草稿切换项目、空间、工作台均需确认；取消继续保留；刷新后读取已保存内容，未保存内容不冒充持久化。
4. 重新生成前明确确认，取消不创建尝试；失败保留已有剧本和来源 brief；生成历史请求失败后可重试。
5. 项目列表/详情读取失败可恢复；创建已被后端接受但响应迟到时切空间，旧响应不污染新空间，切回可读取持久项目。
6. 图像草稿与一次响应未知的图像幂等键，经短剧工作台往返后保持；再次提交返回原图像任务，后端仅一条记录。该回归覆盖产品审查发现并由前端修复的跨工作台丢草稿问题。

新布局覆盖 1280×900、1440×1100、1920×1080、1280×640 短屏、1040×900 单列、390×844 手机。30 个状态测得最大横向溢出 0px、裁切阅读文本 0 处、桌面同排顶部差 0px、侧栏与整页底边差 0px；最长 brief、各场景动作/对白、编辑保存按钮及短屏身份信息可滚动到达。

QA 和主集成实际查看了中文合成数据截图，没有后期图像修改：

- [短剧桌面](screenshots/script-desktop.png)、[短剧手机](screenshots/script-mobile.png)
- [短剧 30 状态几何记录](screenshots/iteration-4-project-geometry.json)
- [图像桌面](screenshots/desktop.png)、[图像手机](screenshots/mobile.png)、[原图像 24 状态几何记录](screenshots/iteration-4-image-geometry.json)（侧栏页底及双列底边最大差均 0px）
- 原始短剧证据 `/tmp/studio-project-evidence-7W8r44`；原图像最终回归证据 `/tmp/studio-layout-evidence-w4SuZU`；PNG 本地协议证据 `/tmp/studio-provider-evidence-NFYESb`。

首轮曾纠正测试按钮名称及 JSON 错误码层级；一次手机保存用例恰遇前端热更新，trace 含 App 模块重载。最终结论只采用代码冻结后的完整回归，不放宽断言。

## 边界

新增剧本由 `mock-script` 程序生成，非真实 AI 创作；仅保存当前剧本，没有版本回滚、分镜、音视频生成。图像真实协议仍只使用本地假上游验证，未验证官方账号、生成质量或账单。演示空间过滤不是生产认证或企业权限隔离。未验证容量负载、断电恢复或生产部署。新增 Actions 的远端执行尚未发生。
