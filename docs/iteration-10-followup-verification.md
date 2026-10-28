# 第十轮补充验收：TikTok PHOTO 与自动刷新（2026-10-28）

本次从本地提交 `50a7500e00fd6ba396354b0df99d976d0e8fd30f` 继续开发。**最终35组中34组通过、1组外部阻塞，无未修复的产品测试失败。** 本报告与证据独立保存，未覆盖上一阶段记录；没有真实OAuth、持久真实凭据、平台上传／发布、付费调用、部署或推送。

完整命令、退出码、耗时和最终343个测试源码文件SHA-256见[机器报告](iteration-10-followup-test-report.json)。原始日志位于本次云工作区 `/workspace/iteration10-followup-evidence/`。所有实现代理及父任务冻结源码后启动完整回归，最后哈希核对无漂移；之后仅整理文档和结果。

## 完整回归结果

| 范围 | 证据 |
| --- | --- |
| Go | gofmt、vet、server、全部cmd、CGO=0构建通过；`go test -race -count=1 ./...`完整通过，184.506秒，包含真实PostgreSQL和原生加密备份恢复。 |
| 前端 | lint、typecheck、build、40单测与1093条中英文静态覆盖通过。 |
| 浏览器 | 普通桌面／手机94项通过，416.203秒；安全认证10项、真实PG与worker10项、精确报价2项通过。新增8项覆盖中英文PHOTO准备、顺序／封面、权利／公开链接同意、版本刷新和未知结果恢复；上轮编辑器、海报、发布中心也完整复验。 |
| 布局 | 基础24、项目30、分镜36，共90状态通过。新PHOTO移动端中英文截图人工查看无横向裁切。 |
| API／安全 | OpenAPI结构、435个实际HTTP合同响应、32个安全响应、27个上一轮新增场景、23个本轮PHOTO安全响应通过。包含CSRF、租户隔离、viewer禁写、版本、幂等、归一化回执、下载拒绝及进程重启。435含轮询，数量随运行耗时变化。 |
| PHOTO实际数据 | 真海报PNG→独立JPEG，原图不变；双线性缩小、白色透明背景合成、尺寸／类型／数量限制；同字节重新归一化与重排不能绕过未知意图去重。真实安全HTTP接受合成已连接账号的本地PHOTO意图，但不发送平台。 |
| PHOTO共享存储 | 实际PG、三个store、独立私有缓存及shared-file对象存储：跨实例海报归一化、精确回执、CAS、PHOTO意图重放、第三实例重建缓存及JPEG解码通过；外租户素材与状态拒绝。 |
| Token与平台合同 | 合成上游验证官方PHOTO顶层AI字段／顺序／封面／直发与草稿协议；JIT刷新绝对有效期、真实PG单次刷新租约、轮换持久化、撤销竞态、提交丢失／未知状态隔离与重启不重发。真实平台抓取与轮换仍未验收。 |
| 媒体与4K | 完整媒体、字幕、恢复检查通过；Go测试含实际关键帧、转场和横／竖／方形2160p解码。另经最终服务实际HTTP导出3840×2160 H.264，0.520996秒，全文件FFmpeg解码通过。此短合成样本不证明15秒上限下容量。 |
| 多服务／治理 | 2 API＋2独立worker、2租户、8并发客户端和16个合成任务通过；PG浏览器与managed-eval接收通过，供应商调用0。Python脚本6项、备份调度7项、live-eval安全21项、本地模型桥接12项通过。 |
| Nginx补充 | 真实隔离官方Nginx容器、network none；新PHOTO与原抖音能力URL、OIDC回调502和SPA回调200，合成cap/code/state未出现在全部日志。首次测试夹具权限问题修正后通过，无产品／网络安全设置变化；不等于全应用双容器验收。 |

## 截图与补充制品

- [合成UI截图ZIP](/workspace/iteration10-followup-evidence/screenshots.zip)：本次最终普通浏览器重跑后归档，包含中英文桌面／手机的编辑器、海报、社交确认、抖音交接、小红书复制及新PHOTO。
- [实际API短4K合成视频](/workspace/iteration10-followup-evidence/synthetic-api-4k.mp4)、[完整解码结果](/workspace/iteration10-followup-evidence/api4k-result.json)。
- [Nginx查询参数日志保护](/workspace/iteration10-followup-evidence/nginx-query-redaction.json)。

素材名称、账号和上游响应均为合成测试数据。模拟已连接、处理或发布状态不能表示真实账号接入、真实社交发布或创作质量通过。

## 阻塞与未运行

**双容器全应用验收仍受容器DNS阻塞。** 上阶段标准构建在后端Dockerfile17行 `apt-get update` 无法解析 `deb.debian.org`，apt100／Compose1；本次按正常配置仅重新执行一次 `docker run --rm debian:bookworm-slim getent hosts deb.debian.org`，退出2且无输出（DNS未解析）；没有再次执行完整构建，也未修改DNS、网络或安全设置。证据为本次 `docker-dns-final.log` 和上阶段 `/workspace/iteration10-evidence/docker-compose-preflight.log`。需要执行环境恢复正常容器DNS后运行 `python scripts/docker_check.py`；宿主机和隔离Nginx通过不能替代它。

真实应用审核、OAuth、token轮换、域名所有权与公开图片抓取、上传和发布终态未运行；外部S3互操作、实际模型权重和真实模型质量仍保留原有外部前提。本次多实例素材验证使用shared-file，不冒充S3。生产部署、公开CI和Docker Hub发布未授权且未执行。

工具链为Go1.25.3、Node24.19.0、Chromium154、FFmpeg7.1.5和真实本地PostgreSQL17.11。任务专用合成数据库已在完整回归后停止；没有修改用户外部账号、真实凭据或云资源。
