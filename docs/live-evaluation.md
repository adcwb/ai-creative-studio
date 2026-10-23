# 真实模型验收工具与授权边界

`scripts/live_eval.py` 默认只生成无网络的 dry-run 报告。支持通过应用的现有创作任务协议，检查 OpenAI、Claude/Anthropic、Google/Gemini、Ark/豆包、MiniMax 的分析文本，以及已实现的图像、语音、视频适配；即梦仅包含其公开图像适配。Nano Banana 不是独立厂商，使用选定的 Google 图像模型与相符协议。具体支持列表在工具 `PROVIDERS` 中，能力真实状态以应用 registry 为准。

工具不是绕过服务端预算门禁的后门。当前运行时若因生产费用治理尚未贯穿所有路径而拒绝开启真实 provider，live 工具同样无法执行；报告应记为未完成。尚未覆盖独立视觉理解、参考图、mask、ASR、强制对齐、口型、音乐生成的真实端到端评测；不能用文本检查通过代替这些模态。

## 无凭据准备

```bash
python3 scripts/live_eval.py --kind analysis --provider openai-text \
  --model synthetic-placeholder --currency USD \
  --budget-micros 100000 --reserve-micros 100000 \
  --report /tmp/studio-dry-report.json
python3 scripts/live_eval_test.py
```

示例型号只是合成占位，不是可用真实模型。父任务可依据官方文档选择热门型号，无需让用户逐项选型；但模型选择不授权费用、账号配置或发送数据。金额为所选币种的百万分之一，示例数值也不是预算授权。

工具使用固定的虚构纸片机器人/花园需求，不读取用户媒体或个人资料；报告含 fixture 哈希、provider/model、检查结果及待人工评价项。既有 `scripts/media_check.py` 和 `alignment_check.py` 生成色块、正弦音等合成媒体，可作为后续经批准的参考素材，不应冒充真实作品。

## 将来获准执行时

必须先获供应商账号、实际模型与预算/数据发送授权，由操作者在隔离应用中临时配置供应商凭据与强制服务端费用保护；本轮没有创建或读取任何已有供应商密钥。不要把 secret 写入命令行、源码或报告。应用须使用安全模式和已认证组织；工具只接受 literal loopback HTTP origin，无代理与重定向。不自动部署或操作云资源。

只有同时提供 `--execute-live --authorize-provider-model-budget` 才会读取标准输入中的一次性 JSON `{ "cookie": "studio_session=…", "csrf_token": "…", "space_id": "…" }`。这些为用户授权会话，由操作者临时输入；工具不从磁盘、环境变量或浏览器查找现有凭据，不持久化会话。`--origin` 应精确等于服务端允许的前端来源。不要复制真实会话到文档或聊天。

执行流程为：验证安全会话和组织；锁定本地 ledger，持久化全额 reservation；创建合成测试项目；仅提交一次绑定 provider/model 的创作请求；只读轮询已接受任务。任何超时、HTTP错误、中断均不自动重试生成，不把失败当作没有费用。工具不会删除测试项目；用户可检查原任务和媒体。

`--reserve-micros` 必须来自操作者核实的单请求保守最大费用，累计 reservation 不得超过 `--budget-micros`。这只是执行器的本地预算上限，**不能约束供应商账单，也不能替代服务端跨客户端并发预算和供应商侧硬上限**。若无法核实最大费用，应保持 dry-run。ledger 使用文件锁/原子替换/fsync，unknown 项在重启后仍占预算，不因任务成功/失败而释放；取得供应商账单并完成审计前不要手改余额或新建 ledger 绕过限制。`--ledger` 和 `--report` 建议保存在仓库外 `/tmp` 或受控审计目录，报告文件必须是新路径，权限0600。

成功仅证明指定任务返回、provider/model匹配及结果引用存在。语义质量、跨镜头角色外观、口型、实际费用均保留人工审查/供应商账单项。报告不存原始错误正文、凭据、远端URL或用户输入；不得从 mock 或本地假上游推导真实质量达标。
