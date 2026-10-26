# 模型验收准备与当前执行边界

`scripts/live_eval.py` 当前仅提供无网络、无凭据读取的 dry-run。报告状态是 `prepared_not_executable`；即使同时指定 `--execute-live --authorize-provider-model-budget` 也在读取会话、发起网络请求或写报告前拒绝。旧工具只绑定全局 provider/model，没有接入租户模型版本、精确请求报价及权威服务端预算，旧执行路径已经删除，不能以用户批准测试额度代替这些实现。

第八轮应用的受治理模型运行时已经接通；缺口是本独立验收工具的接线，不是所有 worker 仍缺治理。不要修改全局环境变量绕开应用护栏。

## 无凭据准备

```bash
python3 scripts/live_eval.py --kind analysis --provider openai-text \
  --model synthetic-placeholder --currency USD \
  --budget-micros 100000 --reserve-micros 100000 \
  --report /tmp/studio-dry-report.json
python3 scripts/live_eval_test.py
```

示例型号只是合成占位，不是可用真实型号。报告必须使用新路径。工具不读取用户媒体；固定虚构纸片机器人素材只用于准备。支持列表是协议分类清单，不表示各真实模型和账户已验证；退役 OpenAI Videos 不在清单内。

当前任务只批准 USD 3 与 CNY 5，各自独立、不能兑换或挪用。CLI 使用整数百万分之一单位，分别强制最高3000000和5000000；其他币种没有本任务付费授权，工具拒绝。应用本身的多币种能力不受此小样工具限制。请求预留必须为正且不超过所选上限，未知费用仍占用；本地账本不能替代权威服务端并发预算。

## 候选计划的真实含义

[`eval/candidate-plan.json`](../eval/candidate-plan.json)逐项分开用户意图 `requested_specification`、实际固定适配器上限 `adapter_contract` 和 `execution_blockers`。全部条目 `harness_supported=false`、`live_authorized=false`，不是可自动执行列表。

例如原文本意图2000输入/500输出不等于当前OpenAI/Anthropic文本合同的64KiB请求、65536输入token保守计价上限和6000输出上限。MiniMax视频4秒480P与现有5秒768P不同，`gpt-transcribe`不兼容仅whisper-1的时间戳协议；不静默更换模型或按较低意图规格估价。

## 后续执行接线仍待实现

需要显式绑定租户模型配置ID/版本，使用相同合成输入取得带哈希及有效期的可信报价，验证服务端原币种预算和账户权限，持久预留后只提交一次；结果未知不自动重发。参考图、mask、ASR、独立视觉、强制对齐、口型、音乐尚无本工具的完整端到端评测。具体模型契约、真实审核与账单来源也须核验。

这些实现及测试完成后，还需要安全凭据和实际认证发送确认才能启用live。不要把凭据放入聊天、命令行参数、前端、Git或报告。USD3/CNY5本轮未使用；没有真实调用、充值、部署或账户变更。
