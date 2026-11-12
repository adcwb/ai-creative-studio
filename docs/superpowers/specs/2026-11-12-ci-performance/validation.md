# 验证记录

2026-11-12，实施提交 `45b4529d8adcba4c59be18303aea3d0bd422618e`（本地，未推送；可用 `git show` 审查）。所有上游均为本地合成服务，真实供应商调用和费用均为零。

环境：Debian 13、Go 1.25.3、现有 npm 锁定依赖、系统 Chromium；临时真实 PostgreSQL **17.11**，CI 仍为 Ubuntu 24.04 / PostgreSQL **16**。本地只用临时 Go wrapper 加 `-buildvcs=false` 处理受限工作树的 VCS stamping；没有修改运行时或把代理、HOME、真实凭据传给合成评估服务。

## 已执行

| 检查 | 结果与实际边界 |
| --- | --- |
| 最新云端失败的本地复现 | i18n 中文状态缺失、偏好 status 二义性、布局长结果离屏 decode 等待均复现；新增的英文加载失败重试也单独复现 |
| 前端静态与单元 | lint / typecheck / 78 tests / build / i18n 2801 条覆盖通过；英文恢复补丁后再次完成这些检查 |
| Go 静态与构建 | gofmt、vet、全部 cmd 构建通过 |
| CI 编排 | actionlint 1.7.12、shell 语法检查通过；11 项 CI 依赖/分区/门禁测试通过，含 failure/cancelled/skipped 拒绝发布；所有 scripts/tests 共 17 项通过 |
| 懒加载图片 | 真实浏览器加载离屏图片成功；损坏图片和停滞图片有界失败；没有吞掉解码异常 |
| 独立布局 | 图像 24、剧本 30、分镜 36 状态通过，含 1920×1080、较小桌面、390 手机、长文本和可到达控件；本地合成服务，不调用供应商 |
| 英文切换 | 原图像/短剧 i18n 4 项通过；后续英文/异步加载/恢复/草稿保留相关 12 项桌面手机用例通过 |
| 普通浏览器 | 全量初跑 107 通过，1 项项目首次挂载 5 秒超时；该逐句字幕—导出用例单独重跑通过（22.7 秒）。没有改断言或放宽超时 |
| 文件模式认证浏览器 | 全量初跑 23 通过，1 项手机 worker 全流程 30 秒超时；此项单独重跑通过（23 秒）。账户隔离、权限、恶意来源、后端配置相关原断言保留 |
| 路由浏览器 | 初跑 80 通过 / 4 失败 / 2 项原有条件跳过；其中两项英文初载恢复为真实新增故障，修复后相关 12 项通过；导航和 P0 截图两项单独重跑通过，新草稿测试两设备通过。没有把这些不同运行写成“单次全量绿色” |
| PostgreSQL 实际验收 | 24 项安全桌面/手机 + 2 项无成本配置验收通过；真实双 API/双 worker、定时备份、托管评估通过；数据库为全新合成数据库，已清理，不使用已有数据 |
| HTTP / 安全 / 恢复 / 媒体 | OpenAPI validator、smoke、contract、secure_contract、story_contract、iteration10、followup、live_eval_test、local_models/test_runner、recovery、media、alignment 全部通过 |
| 文件 Go race | 三个互补分组全部包通过；application 分组分别 478.600 / 296.555 / 217.828 秒。多个本地重任务共享机器，该时间不是独立云端 runner 性能对比 |
| PostgreSQL Go race | 第一组的两项大媒体审核失败已在低负载下原样复验通过（application 29.368 秒）；other 组全包通过（335.56 秒含启动），native 组全包通过（157.19 秒含启动）。第一组其余包/用例均已执行，未删或跳过失败用例 |
| 网关真实请求合同 | 全部 **86 项通过**，26 个 spec/device 子进程；桌面和手机各 13 个 spec 保持独立后端，测试供应商仅 loopback 合成服务 |

## 环境失败和未验证

- 本地同时冷编译多组竞态测试、启动浏览器与 Docker 构建时负载曾超过 15（5 个可用 CPU），出现部分原有 UI 挂载/截图/worker 超时。相关失败已经用原断言分别复验；不调整产品或测试期限来掩盖资源竞争。独立 CI 矩阵 runner 不共享后端和数据库。
- 大媒体审核两项测试在最初 PG 第一组中失败：`TestGatewayLargeSpeechStreamsThroughReviewAndAssetReceipt` 的 allow 分支、`TestGatewayLargeVideoDownloadsIntoReviewedAssetWithoutGenerationRetry` 返回 `execution governance unavailable or denied`。第一次聚焦复验视频通过、语音仍失败；最终负载降低后，两项测试的 allow/deny、哈希与实际媒体回执断言全部通过（application 29.368 秒，总命令 32.91 秒）。没有降低审核保护、增大期限或修改业务代码。
- Docker 两镜像本地构建确实尝试：Docker 配置先改用临时目录以适配只读 HOME；随后后端 runtime 的 apt 因构建容器无法解析 `deb.debian.org` 而退出 100，双容器验收因此未执行。未修改 Dockerfile、降低签名校验或无限重试。最新基线云端该验收通过，不代表本轮 Buildx/GHA cache 编排已在云端验证。
- 新工作流的真实 Actions DAG、runner 排队、PostgreSQL 16、GHA 缓存命中和 Docker 发布尚未执行。本轮明确不推送、不手动触发远端检查、不部署；8–12 分钟为根据历史分组耗时提出的暖缓存目标，不能声称已达到。

## 本地证据定位

详细终端日志位于 `/tmp/studio-*.log`；布局证据分别为 `/tmp/studio-layout-evidence-b2jEkG`、`/tmp/studio-project-evidence-QvN8Bl`、`/tmp/studio-storyboard-evidence-pWewhe`。这些是临时工作区证据，不随 Git 分发；可执行测试和云端历史时间汇总随本规格入仓，便于重新验证。完整真实模型、生产数据、部署和供应商效果验收不在本轮范围。

最终状态：本轮全部测试进程已结束，临时 PostgreSQL 已停止；未遗留卡住任务。最新远端仍为基线 `bd47eef0e2ed5dbcc944f7a7c4376499ec28c791`，原暂停工作树保持干净的 `work@02638f2`。软件修复的本地验收完成；本地双容器因上述 DNS 故障阻塞，修改后的真实云端检查与性能仍待后续授权推送验证。
