# 第九轮候选协议补缺：最终回归

2026-10-26，产品冻结提交 `5fefb195fc2c51d1527d6243d37219245d42c1e1`。本记录属于 `d633c295` 后的补缺验证，不沿用前轮测试结果。之后仅补交付文档。

**本轮实际执行32组，全部通过，无产品测试失败。** Docker双容器与真实S3服务合同另列未运行；不计作通过。逐项退出码、耗时、日志及边界见 [机器可读报告](iteration-9-followup-test-report.json)。未推送、部署、配置真实凭据或执行付费模型调用；USD3与CNY5均未使用。

| 范围 | 本轮结果 |
| --- | --- |
| Go与构建 | gofmt、vet、全部cmd、server及CGO0静态构建通过；完整`go test -race ./... -count=1`通过184.803s，studio包146.522s。配置真实本地PG集成变量，备份源库和空目标库均新建。 |
| 前端 | lint/typecheck/build、31单测、842条中英文静态覆盖通过。 |
| 浏览器 | 普通桌面/手机66项、安全认证桌面/手机10项通过。新增纯转写页面fixture验证转义文本、无时间戳及不能应用字幕；这是合成结果UI测试，不是模型推理。 |
| 布局 | 基础24、项目30、分镜36，共90真实浏览器状态通过。 |
| API及协议 | OpenAPI结构、432个实际HTTP响应、32个安全合同响应通过；轮询响应数量依耗时变化。Provider本地合成集成5次显式请求、零自动重放。新增候选型号、输出规格报价摘要、即梦授权桥接、纯转写wire/HTTP接收/预算/持久化/隔离测试包含在完整race中。 |
| PG与worker浏览器 | 真实PG独立worker桌面/手机10项，精确报价接收fixture2项；managed eval真实API/PG接收合同通过，后者不启动worker、供应商请求0。 |
| 实际媒体 | 7组媒体和4组字幕测试、10字幕API响应通过；MP4完整解码、音视频及字幕流、PCM能量、时长、逐句字幕与重启恢复有实际证据。 |
| 恢复与治理 | 文件4素材恢复与SIGKILL、原生pg_dump/pg_restore、加密bundle恢复、保留规则、合成KEK轮换、unknown费用保持及错误/撤销/回滚包含在最终Go/Python检查中通过。未使用真实密钥。 |
| Python | 既有4项、备份调度7项、managed eval工具21项、具体离线模型12项及py_compile通过。真实模型框架/权重仍未运行。 |

## 实际多服务输出

`/tmp/studio-multiservice-ad6_uxnf`：PostgreSQL17.11、2 API＋2独立worker、各自私有缓存、shared-file对象存储；2租户、8并发客户端、16个合成创作任务，零失败。接受p95为604.58ms、完成p95为5556.75ms，仅为本机合成负载，不是生产容量或模型延迟指标。

测试实际核验crossfade/overlay导出的时长及解码像素、仅叠层引用资源的worker缓存物化、跨租户叠层素材和下载拒绝、跨API幂等、脚本/分镜/分析/字幕独立worker、发送后SIGKILL保持unknown且不重发、成员和会话撤销。此链路使用shared-file，不代表真实S3已通过。

媒体证据 `/tmp/studio-media-evidence-fchezg_6`；字幕证据 `/tmp/studio-alignment-evidence-9hi398u0`；PG浏览器 `/tmp/studio-postgres-browser-6ipc87jx`；精确报价 `/tmp/studio-postgres-browser-v4xb9grn`；managed接收演练 `/tmp/studio-managed-eval-ptwmkxu1`。

## 未运行与准确阻塞

- **Docker本次未重试**：同环境此前匿名官方`debian:bookworm-slim`拉取在`auth.docker.io/token`经`172.31.3.77:53`查询超时。原日志 `/tmp/studio-docker-evidence-2hkr8o1e/compose.log`。没有新网络恢复证据，不将历史失败冒充本次再次执行；静态构建通过不能替代双容器验收。
- **真实S3合同本次未运行**：无合格现有本地服务。本次固定版官方MinIO源码安装解析成功，但`storage.googleapis.com`模块ZIP返回Forbidden，命令exit1；日志 `/tmp/studio-s3-minio-build.log`。下载已停止，无二进制或服务产生。旧rclone不满足原子条件写/Range206的结论不被降级；没有修改网络安全设置或读取云凭据。
- **真实供应商及公网即梦参考桥接未运行**：缺安全认证传输、实际账户/模型/可信报价/对象服务就绪条件；本地fixture证明协议和治理，不证明质量或实际费用。
- **本地真实权重未运行**：Wav2Vec2、Wav2Lip、AudioGen具体桥接仅算法/合成协议/隔离/FFmpeg验证。依赖与审核后的离线包、用途许可及真实质量/资源测量仍待准备，详见[具体离线模型说明](local-model-engines.md)。
- **生产部署、发布、容量及外部SSO/治理/监控灾备服务未运行**；本次没有扩大外部授权。

复现脚本 `/tmp/studio-qa9-followup-runner.py`，日志独立前缀 `/tmp/studio-qa9-followup-*`，未覆盖上轮证据。临时日志不是永久制品托管，仓库保存本报告与JSON汇总。完整测试成功后只提交验证文档。
