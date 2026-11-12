# CI 故障修复与并行检查

日期：2026-11-12。状态：Implementing，本地回归进行中；未推送、未部署，云端提速尚未验证。

基线：`bd47eef0e2ed5dbcc944f7a7c4376499ec28c791`（安全 fetch 后新建 `fix/ci-runtime-improvements`）。原 `fix/consumer-studio-guidance` 与暂停短剧工作树 `work@02638f2` 均保留；本轮不合并暂停功能。不修改生产模型配置、能力勾选、费用或项目额度。

## 问题与验收合同

US-CI-1：开发者推送后应及时获得完整且可信的检查结果，避免串行测试和无界图片等待拖住发布。

| ID | MUST 行为 | 验收 |
| --- | --- | --- |
| FR-1 / AC-1 | 切换语言后，任务状态、保存按钮和页面语言一致；保留异步字典加载、错误恢复、最后选择优先及账户隔离 | Given 英文模拟合成任务已完成并保存，When 切中文，Then 状态与保存标记变为中文，任务数据不变；桌面/手机均通过 |
| FR-2 / AC-2 | 存储不可写时准确报告偏好不能保存；并存的语言加载提示不能造成定位歧义 | Given localStorage 写入失败，When 切主题和英文，Then 具名“偏好保存状态 / Preference storage status”继续报告失败；刷新恢复实际持久值 |
| FR-3 / AC-3 | 全页布局截图主动加载离屏图片，有界等待并校验真实解码；不得吞掉损坏图片错误 | Given 离屏 lazy 图片，When 布局检查，Then 请求、加载和解码成功；坏图和无响应图明确失败；24 个页面状态仍验收 |
| NFR-1 / AC-4 | 保留原有 Go race、两种存储、浏览器桌面/手机、真实 PG、迁移/恢复、租户/安全、网关和双容器覆盖 | 文件与 PG 各自执行原三个互补分组；普通/路由/认证分别覆盖两设备；网关每个 spec/device 独立后端；失败不提前取消其他矩阵单元 |
| NFR-2 / AC-5 | 任一必需检查失败、取消或跳过时禁止发布镜像 | `verify` 汇总全部质量门禁；Browser acceptance / Gateway creation workflows 保留稳定检查名称，矩阵缺失或失败时门禁失败；Docker 只依赖成功的 verify |
| NFR-3 / AC-6 | 缓存不替代验证，也不引入不可信依赖或生产凭据 | 继续锁定 action SHA、Ubuntu 24.04、Go/npm 依赖锁；临时 PG 仅本地 socket 信任且 TCP 拒绝；BuildKit 分组件缓存，容器验收不登录注册表 |

## 实际故障与日志证据

最新 [run 43181014496](https://github.com/adcwb/ai-creative-studio/actions/runs/43181014496) 在上述基线失败；不是上轮已修复的重复导航 href 问题。

1. [Browser job 131056840524](https://github.com/adcwb/ai-creative-studio/actions/runs/43181014496/job/131056840524)：`e2e/i18n.spec.ts:21` 的“模拟完成”未出现，桌面/手机各失败一次，106 项通过。最新懒加载字典实现的 effect 清理在语言更新时把全局同步翻译恢复为父级语言，因此 hook 翻译与模块级状态 helper 不一致。**旧文案仍是正确产品合同，不删断言来过 CI。** 本地修改前已复现；把父级恢复限定为 provider 卸载，保留每次提交语言对 document.lang 的更新。
2. [PostgreSQL job 131056840324](https://github.com/adcwb/ai-creative-studio/actions/runs/43181014496/job/131056840324)：`appearance.spec.ts:121` 的 `getByRole('status')` 同时匹配偏好保存提示和新增 `.locale-feedback`。数据库本身不是根因。本地文件认证路径也复现。给实际偏好状态补中英文无障碍名称，测试定位此 live region，继续断言禁止持久化后的恢复行为。
3. [verify job 131056840521](https://github.com/adcwb/ai-creative-studio/actions/runs/43181014496/job/131056840521)：30 分 16 秒后取消，停留在 Responsive layout regression。`layout_check.mjs` 在未请求的离屏 lazy 图片上无限等待 `decode()`，并吞掉解码错误。本地原检查停在 `long/desktop-1280`，90 秒外部诊断期限后终止。修复只在测试页将图片改 eager；10 秒内检查 complete/naturalWidth，再限时解码并提供状态诊断。布局步骤再设 5 分钟总期限，不靠放宽超时掩盖错误。
4. 跑到此前被前置失败挡住的路由测试后，本地还复现 `performance.spec.ts` 的初次 English load failure recovery 失败。重置业务 Promise 无法可靠清除当前浏览器的失败模块图；按钮重复相同 import 后仍显示失败。现在仅当 editable children 尚未挂载时允许刷新重试；已有页面则明确提示先保存再刷新，且可切回中文，避免丢失草稿。新增失败切换保留草稿测试，初次恢复和切换的相关 12 个桌面/手机用例通过。这是后续本地发现的故障，不归因于尚未执行它的云端 job。

该运行的网关和双容器检查成功，Docker 发布跳过。后续本地修复未触发新的云端运行。

## 性能证据与实现

原始 job/step 时间见 [timings.json](timings.json)，来源为 GitHub Actions API。运行排队、runner 冷热及用户感知等待不同，不能拿失败提前退出的运行冒充提速。

| 项目 | 成功 run 42777274159 | 最新失败 run 43181014496 | 本轮变化 |
| --- | --- | --- | --- |
| 完整流水线 | 19 分 18 秒 | verify 30 分钟期限后取消 | 先修无界等待，再缩短关键路径；尚无修改后云端实测 |
| verify | 17 分 25 秒；Go checks 635 秒 | Go checks 584 秒 | 格式/vet/前端、Go race、HTTP/布局/媒体合同独立并行 |
| PG | 16 分 25 秒；Go 698 秒 | Go 753 秒 | 三组 race 各自独立 PG runner，PG worker/restore/browser 验收独立运行 |
| Browser acceptance | 14 分 57 秒 | 本轮第一个套件两项失败，后续未跑 | e2e/routing/auth × desktop/mobile 共 6 runner，单个 runner 仍为一个 worker |
| 网关 | 12 分 01 秒 | 8 分 35 秒 | 两设备分 runner，仍逐 spec 启新后端，不弱化注册防滥用 |
| 双容器 / 发布后端 | 1 分 58 秒 / 1 分 48 秒 | 1 分 45 秒 / 跳过 | Buildx 本地 load 验收与发布共享分组件 GHA 层缓存；发布前仍全套验收 |

另一个成功运行 [42265098522](https://github.com/adcwb/ai-creative-studio/actions/runs/42265098522) verify 为 15 分 04 秒，说明耗时本来就受环境影响。最新依赖准备约 31 秒；主要瓶颈是串行测试，不是再次替换安装源。

实现约束：

- `scripts/ci_go_tests.sh` 保留 `^Test[A-M]`、排除该组和 Native 的其余组、`^TestNative` 三个互补选择；其余组继续包含 Examples/Fuzz seeds。`-race -count=1` 保留竞态检查并避免复用数据库测试结果缓存。两种存储共 6 个 runner，每个 PG runner 有独立临时数据库和备份 source/target。
- `scripts/ci_browser_tests.sh` 只接受三个已知套件和两设备，明确 workers=1。`gateway_browser_check.mjs` 新增校验过的 `STUDIO_GATEWAY_DEVICE`；默认本地命令仍跑两设备全部 spec。没有增加同一后端并发或注册 allowance。
- 独立 integrations job 保留进程崩溃、provider、project、storyboard、布局、OpenAPI/HTTP、治理工具、离线恢复、媒体和字幕对齐的全部原命令。durable job 保留定时备份、两 API/两 worker、完整安全浏览器与无费用配置 fixture、托管评估。
- Go/npm/官方 apt 缓存沿用原方案；OpenAPI 工具新增 requirements 文件和 setup-python pip 缓存。没有缓存 node_modules 或测试结果，也没有跨 job 复用活跃服务/数据库。
- Docker build-push-action 使用 `scope=studio-backend/frontend`，验收阶段 load=true/push=false；发布阶段复用层而非跳过构建。缓存导出失败可继续完成真正构建/验收，并限制为 2 分钟，缓存内容不作为测试通过依据。[官方缓存协议说明](https://docs.docker.com/build/cache/backends/gha/)。
- push 和 PR 触发覆盖保持，新增手动检查入口；同分支旧运行继续由 concurrency 取消。手动运行不发布镜像。没有 paths-ignore 或跳过关键检查的快速通道。
- 最大同时约 18 个重任务 runner；额外三个汇总门禁只在依赖完成后短暂运行。总 runner 分钟可能因重复初始化略增，墙钟时间依赖可用并发、缓存和上游服务。根据原分组/套件耗时，暖缓存目标约 **8–12 分钟含发布**，这是估算，不是已验证指标或服务承诺。无可用并发时可能排队。

## 实施与验证清单

- [x] T-1（AC-1/2）：先复现，再修语言生命周期和状态定位。
- [x] T-2（AC-3）：有界图片加载、正常/损坏/停滞图浏览器回归及完整布局检查。
- [x] T-3（AC-4/5/6）：拆分矩阵、隔离 PG、稳定汇总门禁、缓存；启动脚本与门禁负向测试。
- [ ] T-4：完成本地可执行回归，记录任何未执行/环境失败。
- [ ] T-5：本地提交并核对保留工作树；未经本轮后续授权不推送、不部署。
- [ ] 后续云端验收（本轮未授权推送）：一次冷缓存及至少两次暖缓存完整运行，对比从首个 job 启动到最后镜像结束的 elapsed、队列时间、失败传播与 runner 分钟，确认目标和仓库并发资源是否匹配。

本轮没有数据库或业务数据迁移。回退时恢复 CI 编排与前端文件即可；缓存失效可重建。真实付费供应商与生产部署不属于本轮验证范围。
