# 第三轮独立验证记录

环境为指定工作区 `/workspace/ai-creative-studio`，日期 2026-10-22，基线 `6e30ff6`。没有推送、PR、部署、真实模型凭据或付费生成调用。所有测试进程显式选择 mock，或运行只能注入本地 loopback HTTP 的测试二进制；不会继承宿主的真实 provider/key 设置。

## 已实际执行

| 检查 | 结果与执行者 |
| --- | --- |
| `go test -race -count=1 -timeout=60s ./...` | QA 通过 11.920s；末次快照缺字段修复后由后端所有者再次完整通过 12.562s，无数据竞争；测试 harness 普通运行默认跳过，由下述进程脚本显式运行 |
| `go vet ./...` / `go build ./...` | 后端所有者最终通过；主集成另构建 `bin/server` 用于 smoke |
| 前端 lint / typecheck / unit / build | QA 最终均通过，2 文件 7 项单元测试；包含历史 provider 标签、配置就绪边界及重试费用不确定性 |
| 原有桌面/手机 E2E | 最终 16/16 全部通过，1.3 分钟；首轮两处旧测试的宽泛按钮选择器误点新增的“重新检查图像服务”，已改成精确“重新查询”，没有放宽业务断言 |
| `node scripts/provider_check.mjs` | QA 完整通过；本地假上游收到 5 次明确创建/重试的请求，所有幂等重放、配置守卫拒绝、能力未知及进程恢复均未增加上游调用 |
| `node scripts/layout_check.mjs` | QA 最终 24 个浏览器状态通过；双列底边最大差 0 CSS px，侧栏与整页底边最大差 0 CSS px |
| `python3 scripts/smoke.py` | 主集成在末次快照修复后重建生产二进制并再次运行通过；mock 的 SIGKILL 恢复执行、retry 关联、素材、搜索、幂等完整链路 |
| 真实 HTTP OpenAPI 契约 | 架构与主集成运行仓库 `scripts/contract_check.py`，103 个实际响应（含轮询）通过；能力、guard 400/409/503、历史读取及跨配置已接受键重放 |
| OpenAPI 结构校验 | 主集成通过 `python3 -m openapi_spec_validator api/openapi.json` |
| `git diff --check` 与 Node 脚本语法检查 | QA 通过 |
| 依赖 | 主集成 npm audit 0 项，原始报告 `/tmp/studio-round3-audit.json`；package/lock、go.mod/go.sum 相对基线无变更 |

复现环境前缀：`PATH=/tmp/studio-go125/go/bin:$PATH GOPATH=/tmp/studio-gopath GOCACHE=/tmp/studio-gocache`。浏览器命令可另加 `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/usr/bin/chromium`，不指定时使用 Playwright 已安装的 Chromium。Python 契约命令在本机另加 `PYTHONPATH=/tmp/studio-openapi-validator`。两个新 Node 脚本复用已有 Playwright 依赖，用随机本地端口、临时目录并在结束后清理进程和数据。

## 协议、执行和存储

`provider_test.go` 使用 `httptest`，验证三种尺寸的实际请求经过 Eino、归一化输入、固定模型及 7 个请求字段、Bearer 服务端密钥、kind/style 进入提示词，且不发送不支持的 `response_format`/`style` 字段。请求字段依据 [OpenAI Create image 官方文档](https://developers.openai.com/api/reference/resources/images/methods/generate/)；测试没有调用官方地址。

非法输入在发送前拒绝。假上游依次返回 URL-only、多图、非法 JSON/base64、伪装 PNG、截断图片、错误尺寸、超像素头、超过 16 MiB 图片和 24 MiB JSON，均失败且不抓取返回的 URL。401/403、429、其他 4xx、5xx、超时、redirect 分别安全分类，无自动重试或重定向请求。生产构造器固定官方地址、120 秒超时、不继承环境代理；测试只检查构造配置，不调用它。

`provider_store_test.go` 与 `provider_guard_test.go` 验证：

- PNG MIME/尺寸/模型和素材记录跨重启保留，真实结果 HTTP 返回原始 PNG；文件为受限结果目录内的随机 ID `.png`，权限 0600。
- 拒绝跨空间读取/保存、结果路径篡改、符号链接与 FIFO；FIFO 快速失败。公共任务 JSON 不暴露本地路径或幂等键。
- 无效 PNG/MIME 进入安全失败状态；文件写入或状态提交失败不会发布成功任务/结果 URL，也不能保存素材。已终态任务不能再 Finish 覆盖。
- 真实 running 重启变为 `failed/execution_interrupted`，mock running 继续原有 queued 恢复；queued provider/model 与当前执行器不同会失败且不调用 provider。
- 配置不可用时禁止新建/重试；已接受的相同键、输入及父任务重放优先返回原任务，不重绑 provider。
- `expected_provider`/`expected_model` 无效组合拒绝且无入队；新请求与当前配置不符返回 409 `provider_changed`，普通创建与 retry 都守卫；已接受键的重放忽略新的 expected 值。
- 任意上游错误中的合成密钥和私有 body 不进入任务、状态快照或用户错误。
- 架构审查发现已有快照缺顶层字段会被预设默认值误当空库；后端修复后，7 种 `{}`、整体 null、缺 version/tasks/assets、map 为 null 的快照均拒绝启动且原文件字节保持不变，防止静默丢失幂等记录。

## 真浏览器 PNG 与进程中断

`provider_harness_test.go` 仅存在于 `_test.go`，默认跳过。`scripts/provider_check.mjs` 编译测试二进制，注入 literal loopback 上游和固定合成密钥，运行实际 Handler、FileStore、Worker 与 Eino。生产二进制没有测试 endpoint 或假服务开关。

脚本先在 mock 模式保存历史 SVG，浏览器缓存 mock 能力，再重启 API 为真实协议配置。首次点击携带旧 mock 预期得到 409，服务器没有产生上游请求；页面更新能力和费用提示，不自动重发。用户再次点击保留原业务幂等键，实际返回 PNG，浏览器图片 naturalWidth 为 1024，详情展示模型与尺寸，保存后重启仍能读取同一 PNG 字节和素材。历史 SVG 仍显示“模拟完成”。

随后模拟后端已接收但响应丢失，前端保留草稿与键；能力请求失败时按钮禁用、程序触发点击也不发 POST。能力恢复后原键重放，不产生重复执行。另验证上游 429 脱敏；在上游已收到请求、响应仍挂起时 SIGKILL API，重启后任务为 `execution_interrupted`，等待 worker 周期与原键重放均无第二次调用；明确 retry 新键才创建关联子任务并调用一次。最后切换缺密钥配置，新请求 503、已接受键仍 200，历史可读。合成密钥/私有错误 body 均不在状态文件、API JSON 或进程日志中。

可审阅证据已归档为 [本地协议验收记录](screenshots/iteration-3-provider-evidence.json)；原始证据 `/tmp/studio-provider-evidence-Fu2hn3/evidence.json` 与同目录 `local-protocol-fixture-desktop.png` 保留。该图提示词明确写“本地协议测试样本，非模型生成”；图像是脚本生成的渐变 PNG，不能作为 OpenAI 真实生成画质或账号可用性的证明。

## 布局测量与截图

基线证据来自仓库已有合成浏览器截图。Library 用户图片的下载未成功，本轮没有查看到可读的用户原图，也未把用户图片放入仓库。基线 1440px 页面中侧栏背景在 y=1100 结束，两个主面板底边相差约 65px。

最终矩阵为 1280×900、1440×1100、1920×1080、1280×640 短屏、1040×900 单列、390×844 手机，分别运行空列表、一个完成结果、长描述/三个任务列表、失败任务编辑态，共 24 个状态。全部没有横向溢出或任务描述裁切；双列上下边对齐、单列不重叠、编辑提交按钮和末尾详情可滚动到达、短屏侧栏底部身份信息可达。1440px 单结果状态两面板底边均为 1246.265625，侧栏和 shell 底边均为 1323.859375；长列表与编辑态两面板底边均为 2317.03125，侧栏和 shell 均为 2394.625。

原始最终证据 `/tmp/studio-layout-evidence-gDVias/geometry.json` 与同目录 12 张浏览器截图。QA 实际查看了 `completed-desktop-1440.png` 与 `completed-mobile-390.png`，页面自然延展，未做图像后期修改。拍摄前失焦并滚动到顶部；与基线使用相同模板及桌面视口，模型能力提示增加导致整页高度略变。主集成归档了 [桌面截图](screenshots/desktop.png)、[手机截图](screenshots/mobile.png) 和 [24 状态几何记录](screenshots/iteration-3-geometry.json)。

## 证据边界

已验证官方协议适配、真实业务后端、Eino、PNG 文件及进程恢复；未使用真实 API key，未验证远端凭据、额度、模型生成质量或真实账单。5 次本地假上游调用与零自动重放不能保证远端“绝不重复计费”：网络超时或进程中断时上游可能已完成且计费，显式 retry 可能再次收费。现有 demo-user 和 space_id 过滤不是生产认证/企业权限隔离。没有验证容量、断电耐久性或生产部署，也没有触发远程 CI。

## 授权发布后的 CI 回归

用户随后单独授权第三轮公开推送，已正常发布 `d1cae676da8840da38b0f9e0f08a0691ea686f49`。上文“未推送/未执行远程 CI”是本地验收时的记录。首次 [CI](https://github.com/adcwb/ai-creative-studio/actions/runs/39757001460) 的 Go、smoke、前端、16 项 E2E 与 Provider 联调通过，布局检查在页顶复位准备阶段失败（scrollY 475，而非 0），尚未进入该状态的几何断言；后续 OpenAPI 步骤被跳过。

模板/重试选择通过 requestAnimationFrame 聚焦输入框，测试原先只立即失焦和滚动，可能先复位再被延迟聚焦滚动。修复测试：先等待两帧、字体和图像解码，再失焦与复位。保留 scrollY=0、全部几何、溢出、裁切及控件可达性断言，不修改业务 UI 或放宽阈值。修复后本地 24 个布局状态全部通过（`/tmp/studio-layout-evidence-68qDgR`），Node 语法及差异检查通过。修复提交的远程结果应以该提交对应 GitHub Actions 为准。
