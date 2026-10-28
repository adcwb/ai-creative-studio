> 本文为首个里程碑 `50a7500` 的历史验证。图片发布与令牌续期追加实现的最终验收见 [后续验证](iteration-10-followup-verification.md)。

# 第十轮本地验收（2026-10-28）

本轮从已发布 `b6b3d83f56c90175af03bb3dd0e3db24a20caefd` 开发，验证的是本轮工作树，不复用上一轮 CI 结果。**最终34组中33组通过，1组双容器验收被构建容器的 DNS 阻塞；没有尚未修复的产品测试失败。** 另有真实 HTTP 4K 导出与隔离 Nginx 查询参数日志检查通过。没有推送、发布镜像、部署应用、配置真实凭据、执行真实 OAuth 或对外发布。

完整命令、退出码、耗时、最终源码 SHA-256 与增量复验依据见[机器报告](iteration-10-test-report.json)。原始日志和可复现验收脚本保存在本次云工作区 `/workspace/iteration10-evidence/`；它不是公共制品托管地址。测试环境为 Go 1.25.3、Node 24.19.0、Chromium 154、FFmpeg 7.1.5、真实本地 PostgreSQL 17.11。

## 通过的检查

| 范围 | 最终证据 |
| --- | --- |
| Go 与构建 | gofmt、vet、server、全部 cmd、CGO=0 静态构建通过。`go test -race -count=1 ./...` 完整通过160.948秒，studio包132.767秒；配置真实 PostgreSQL 集成与原生备份工具，没有用数据库 mock 替代这些检查。 |
| 前端 | 最后小红书主动复制功能加入后，重新执行 lint、typecheck、build、40单测、1041条中英文静态覆盖，全部通过。 |
| 浏览器 | 最终普通桌面/手机86项通过393.276秒；安全认证10项通过。另有真实 PostgreSQL＋独立 worker 桌面/手机10项、精确报价接收2项通过。普通最终86项覆盖新增编辑器、海报、TikTok 同意及回调、抖音交接和小红书主动复制的中英文界面。 |
| 布局 | 基础24、项目30、分镜36，共90状态通过。新功能另有桌面/手机溢出检查、24张合成 UI 截图及人工检查。 |
| API 与授权 | OpenAPI 结构、434个实际 HTTP 合同响应、32个安全合同响应、27个新增安全 HTTP 检查通过。新增检查包含海报实际 PNG、跨租户、CSRF、viewer 禁写、版本冲突、原键重放、进程重启恢复、成员撤销后下载拒绝，以及未配置时禁止直发。434包含轮询，数量随运行耗时可能变化。 |
| 媒体 | 关键帧运动、线性透明度、原透明通道相乘、结束后消失，五种空间转场及混合转场时钟实际解码验证通过；横屏3840×2160、竖屏2160×3840、方形2160×2160均生成并解码取样。另有7组完整媒体检查和4组字幕检查通过。 |
| 真实 HTTP 4K | 独立补充测试从实际 API 保存2160p时间轴、提交导出、下载 H.264 MP4，得到3840×2160、0.520996秒，并对整个文件运行 FFmpeg 解码。仅为短合成素材，不能当作15秒上限下的容量或质量测量。 |
| PostgreSQL 与恢复 | 2 API＋2独立 worker、共享文件 BlobStore、2租户/8并发客户端/16个合成创作任务通过；跨实例幂等、撤销、媒体合成及发送后 SIGKILL 保持 unknown 且不重发通过。原生 pg_dump/pg_restore、加密备份恢复、保留和合成 KEK 轮换包含在 Go/Python 检查中。测试集群已停止。 |
| 社交与凭据安全 | TikTok OAuth 会话绑定、加密 token、状态查询/撤销（未实现自动 token refresh）、发送前再授权、未知创建结果不重发、恢复查询及冷却；抖音官方 client_token/ticket 缓存与并发刷新、短期能力 URL、撤销/范围读取等使用假上游和合成密钥验证。浏览器中没有真实 OAuth、App 跳转或公开发布。 |
| Python 与治理 | 脚本6项、备份调度7项、live-eval安全21项、本地模型桥接12项通过；managed eval使用真实 API/PG接收，worker未启动、供应商调用0、费用0。Provider合成集成5次显式请求，零自动重放。 |
| Nginx 日志 | 使用现有官方 Nginx 镜像、无网络、不可用本地上游，实际检查素材能力 URL 502、OIDC callback 502、前端 callback 200；合成 cap/code/state 标记不出现在全部容器日志。此项不等于前后端双容器验收。 |

## 截图与实际媒体

- [24张中英文桌面/手机截图 ZIP](/workspace/iteration10-evidence/screenshots.zip)，仅含合成 UI，不含凭据或测试数据目录。
- [实际 API 导出的短4K合成 MP4](/workspace/iteration10-evidence/synthetic-api-4k.mp4)与[探测及完整解码结果](/workspace/iteration10-evidence/api4k-result.json)。
- [隔离 Nginx 检查结果](/workspace/iteration10-evidence/nginx-query-redaction.json)。

截图包括实时预览、海报、默认发布中心、TikTok 合成确认流程、抖音合成交接、小红书主动复制；模拟已配置的平台界面不表示真实账号已经接入。合成色块、正弦音和 fixture 不代表模型创作质量。

## 失败、未运行与外部阻塞

**Docker构建实际失败，最终双容器应用验收未运行。** 官方基础镜像匿名拉取成功，但标准 `python scripts/docker_check.py` 在后端 Dockerfile 第17行执行 `apt-get update` 时，构建容器无法解析 `deb.debian.org`；apt退出100、Compose构建退出1，前端 npm 与 Go模块下载随后取消。最终使用普通临时 Debian 容器执行 DNS 查询仍退出2。原始证据为 `/workspace/iteration10-evidence/docker-compose-preflight.log` 和 `docker-dns-final.log`。没有修改 Docker DNS、网络或安全配置，没有降低断言。需要执行环境恢复正常容器 DNS 后，重新运行既有标准双容器门禁；宿主机测试和隔离 Nginx 检查不能替代它。

以下仍未验证，不能称为生产或平台审核验收完成：

- TikTok/抖音实际应用权限、真实 OAuth、账号连接、远端上传、审核终态及手机 App 实际交接；小红书 SDK 接入仍受平台暂停接入限制。本轮只验证官方适配合同和安全状态机，不制造真实发布成功。
- 真实模型调用、质量和费用；离线真实权重、真实 S3 服务互操作及公网素材拉取仍保留此前边界。本轮真实多服务使用 shared-file。
- 生产部署、容量、外部 SSO/监控服务，以及本轮公开 CI 和镜像发布。仅本地测试和提交获授权。

## 最后变更后的复验依据

初次采集源码哈希后，四个社交 Retry-After 文件在03:57:29 UTC前完成修改；完整 uncached race 在03:57:58开始，覆盖最终后端。随后又重跑 gofmt、vet、server、全部cmd及静态构建。Nginx最后仅增加日志保护路径，由真实隔离容器独立复验。最后小红书本地复制功能只修改三个前端/浏览器测试文件，重新执行全部六组前端检查和完整普通86项E2E；未改变后端、认证或PG路径，保留其已通过证据。最终逐文件哈希与工作树相符，无测试源码漂移。之后只补文档与测试汇总。
