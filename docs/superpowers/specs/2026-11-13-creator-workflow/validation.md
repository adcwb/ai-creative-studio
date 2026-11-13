# 验证记录

本地环境：Go 1.25.3、Node 24、Chromium、FFmpeg/FFprobe、独立 PostgreSQL 17。全部使用临时数据、合成账号和本地素材，不使用生产配置或付费模型。

已核验：

- `go test ./... -count=1`：全包通过（application 257.138 秒）；新增后续改进另跑受影响回归。
- 新增文件/真实 PostgreSQL 恢复与团队测试，以及既有修订、RBAC 和系列相关 race 回归通过。覆盖 CAS、原请求重放、后续编辑保留、当前撤权、损坏素材、旧多集隔离、跨租户、所有者禁止退出及第二 API 即时失权。
- 前端单元 24 个文件、78 项通过；最终类型、lint、构建（7.71 秒）、2890 条 i18n 消息、Go vet、11 项 CI 分区合同检查均通过。
- `contract_check.py`、`secure_contract_check.py` 与 OpenAPI 静态验证通过；安全合同最终验证 271 个响应，扩展了恢复、参考版本、制作事实和团队操作的真实响应验证。
- `creator-workflow.spec.ts` 桌面 1920×1080、手机 390×844 各 6 项通过；既有 `revisions.spec.ts` 两设备共 6 项通过。最终素材对比预览改进后，两设备各 6 项再次通过（桌面约 1 分钟，手机 52.9 秒）。
- 已实际查看桌面季集/空间及手机资产页面截图，沿用既有配色、字体、卡片和导航；自动检查无新增横向溢出。截图为 `/tmp/creator-{restore,references,season,spaces}-{desktop,mobile}.png`，测试会重新生成。

旧 routing 全量首轮：86 项通过、2 项既有手机适用性跳过、2 项桌面等待超时（切换分集、跨页面主题/语言）。保留原断言和 30 秒超时未调整；随后将两个文件完整独立复验，两个设备共 8 项全部通过（约 1.2 分钟）。超时未能独立复现，不将首轮记为全量通过；推送后继续核验独立 runner 上的完整矩阵。

最终新增相关文件/PG race 回归 37.716 秒通过；真实 PostgreSQL 第二 API 退出即时失权测试在其中通过。截图核验样本 SHA256：

- 桌面季集 `ef73943dd4794799188f72de34d2efcb72484ad3e98021279e1b3e580a926c25`
- 桌面空间 `2d4eb82009e072a1a51f03a89bbca6000a873c2cad8650e8a1ebb8516a909866`
- 手机参考 `e601e79d7f347d3ecfbea2e6676b6e79a5a05d850615facfba0d888ad93c2555`
- 手机恢复 `62e854199c425e930a6ceab9e1e0963e710021ea43cc1a43b9172a07590f5404`

远端源码提交 `486ef479b4906aa6c3ddc6a37516d33c0da818de`，已用 `git ls-remote origin refs/heads/master` 核验。推送后的 [CI 43475422718](https://github.com/adcwb/ai-creative-studio/actions/runs/43475422718) 于 2026-11-13 17:42:21–17:52:22 UTC 完成，25/25 success，约 10分01秒。Go 文件/PG 六组 race、两端 e2e/auth/routing、两端网关、API/媒体/布局、PG worker/restore、双容器、Windows 文件与 PG 原生及 SCM 均成功；既有 Windows 选择性测试矩阵并不代表每一个新增用例都在 Windows 单独执行。

网关日志证实新增六项流程在云端桌面 15.7 秒、手机 12.9 秒通过。完整云端两端 routing 均成功，首轮本地等待超时未复现，不修改断言或超时。

Docker Hub 前后端发布步骤均成功，提交标签 `sha-486ef479b4906aa6c3ddc6a37516d33c0da818de` 及默认分支 latest 已生成：

- backend：`sha256:a6aef9628def6300e8c44c2d00ffbc3ac3da2e1304f0357419ec54de7d6adb03`
- frontend：`sha256:9c858c921397da4e0558965d9c452800850a33981235adbbea93b578de02f356`

以上只是代码与镜像发布，没有部署应用、执行生产迁移或调用真实付费模型。本节 CI 归档补记随下一阶段文档先保留本地，不另触发一次仅文档的完整发布流水线。

真实付费模型、许可、真实外部发布、真实 S3、Windows 原生新功能回归、真实多集创作和画质验收未在此环境执行。本次云端原生 Windows 已在现有选择性矩阵通过；真实服务、许可和效果仍不能用该结果或交叉编译冒充。
