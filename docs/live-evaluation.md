# 模型验收工具与当前执行边界

`scripts/live_eval.py` 默认仅提供无网络、无凭据读取的 dry-run。现在可通过应用的受治理运行时做**单个明确授权请求**：绑定租户模型配置ID/版本、具体候选case、当前精确报价和原币种预算。旧全局 provider 路径已删除，不会因为配置默认模型或环境变量而静默付费。

本轮测试全部使用合成会话、假HTTP transport、合成价格、PNG和音调；没有运行真实模型或读取真实凭据。候选计划 `live_authorized=false` / `execution_authorized=false` 仍准确记录当前缺少认证发送确认，不能把工具支持当作本任务已获live授权。

## 无凭据准备

```bash
python3 scripts/live_eval.py --kind analysis --provider openai-text \
  --model gpt-6.1-sol --case-id case-01 --currency USD \
  --budget-micros 3000000 --reserve-micros 100000 \
  --report /tmp/studio-dry-report.json
python3 scripts/live_eval_test.py
```

这里的预留金额仅演示工具输入，不是此型号实际费用。dry-run报告为 `prepared_not_authorized`，包含实际adapter合同、case阻塞项和合成内容。型号不验证账号开通或协议兼容性，不会联网询价。

## 明确执行流程（本轮未执行）

实际执行之前，操作者需要安全凭据、当前模型资格、可信报价/审核/账单路径，以及用户对实际认证发送的确认。应用必须已使用安全模式、租户隔离和权威预算。工具**不设置或提高预算，不录入凭据，不注册账号**。

在上述前置项就绪后，执行必须额外指定：

- `--execute-live --authorize-provider-model-budget --authorize-session-transmission`，分别确认所选case/模型/金额和会话的认证传输；缺任何确认在读取会话前拒绝。
- `--model-config-id` 和 `--model-config-version`，必须与服务端当前可用目录、provider/model/capability精确一致；不采用默认模型、不fallback。
- 通过标准输入临时提供一次性JSON `{ "cookie": "studio_session=…", "csrf_token": "…", "space_id": "…" }`，最多16KiB。不得将真实会话放入聊天、命令行参数、Git或报告。
- speech须有显式获权 `--voice`；图像默认 `--scenario original`，参考场景使用 `--scenario reference`。每次只提交一次生成，两个场景要分别授权、分别取得报价与保留费用。

工具只连接字面量loopback HTTP应用origin，禁代理和重定向；`--origin`须等于应用允许的前端来源。先验证安全会话/组织及managed目录，再检查服务端该币种预算，创建全新的合成项目。reference场景仅上传工具生成的32×32几何PNG，不读取本地用户文件。

随后针对真实将提交的input取得 `POST /api/v1/runtime-quote`，验证配置ID/版本、`studio-request-v1`、64位十六进制摘要、来源、有效期、原币种与上限；再次检查目录和预算，持久写入本地unknown预留，然后携带相同input、配置版本、quote摘要及上限提交一次。之后只读轮询已接受任务，超时/HTTP错误/响应不明不重发。

## 两种独立预算

本任务只批准 **USD3与CNY5，各自独立、不兑换、不挪用、不充值**。CLI金额单位为整数百万分之一，最高分别3000000/5000000；live要求整数分（10000微单位），不静默四舍五入。其他币种未获本次小样授权，工具拒绝，不影响应用已有多币种能力。

服务端预算的币种和minor units必须匹配，其总limit须不大于本次批准上限，剩余available须覆盖显式预留；实际精确quote必须不超过预留。并发预算在服务端事务内再次检查。操作者必须保持服务端预算不被其他管理员擅自提高；工具不是供应商侧扣费硬限额，也不能担保上游计价正确。

本地ledger还累计所有unknown预留，按币种隔离、文件锁、私有权限、拒绝符号链接、原子替换和fsync。即使任务成功也不自动结算；只有可信供应商账单能证明实际费用。不可新建ledger或手改余额来绕过未知占用。现有报告路径在任何认证读取/请求前拒绝，阻止不确定运行被同报告重放。项目、参考素材及报告不自动删除。

## 候选覆盖与明确拒绝

[`eval/candidate-plan.json`](../eval/candidate-plan.json)区分 `requested_specification` 与 `adapter_contract`：

- case-01/02/03/04/05：文本单次分析。原2000输入/500输出意图不是实际wire限制；当前为64KiB请求和6000输出参数上限。OpenAI/Anthropic的有界报价保守使用65536输入token，其他厂商须精确请求报价。Gemini 3.8的思考参数及总输出边界已复核，仍不能沿用原较低规格估价。
- case-06/07/08：OpenAI／Google Interactions／Ark图像，original/reference分别单次；合成PNG与quote绑定。型号特定参数的官方依据与fixtures见后续核对记录。
- case-09：即梦managed参考桥接已补本地授权接线，但公网S3访问与供应商取图仍未验证；计划继续在认证读取前拒绝，不通过传入全局变量绕过。
- case-10：Google官方模型卡规定原始视频3–10秒／720p，以最大10秒报价；**不保证恰好10秒**，不猜测未核实的duration参数。单次text-to-video，不延长。
- case-11/12：MiniMax H3-Lite为4秒／480P，Ark Seedance mini为5秒／480p；候选规格纳入精确报价摘要，旧报价不能复用。
- case-13：MiniMax speech，显式voice，合成提示最多200字符。
- case-14：`gpt-transcribe`的显式纯文本转写，JSON响应、最多30秒音频/2000字符，结果没有时间戳。不能当作Whisper字幕对齐，不自动更换模型。工具生成音调只检验传输协议，不能证明语音识别质量；无语音的空转写可被拒绝。

上述支持以当前候选计划和最后回归为准；“harness_supported”只表示本地执行链可用，所有真实账户、模型输出与计费仍未验证。OpenAI Videos/Sora继续本地拒绝；即梦视频未核实可用官方接口，仍不接入。

独立视觉、mask、带时间戳ASR、强制对齐、口型、音乐尚无本工具完整真实端到端评测。报告只验证任务状态、provider/model与结果引用，不评价图像像素、语义质量、一致性、口型或费用。真实模型效果、耗时与账单均未验收。

## 本地测试

`python3 scripts/live_eval_test.py`覆盖安全会话前拒绝、无网络dry-run、缺失/过期/不符quote、配置更新、服务端超预算、USD/CNY不兑换、合成PNG引用、voice和字符上限、发送超时只提交一次且unknown保留、账本/报告文件安全及错误脱敏。全部使用内存HTTP stand-in，没有付费模型调用。专项测试不替代整个应用最终回归。

另有实际服务端合同演练 `scripts/managed_eval_check.py`：用明确本地PG socket参数创建随机命名测试库，仅启动API，永不启动worker；注入合成KEK/密钥和固定deny审核器，真实跑session→目录→预算→精确quote→creative接收，断言队列 `queued:0`、服务端reserved及本地unknown，再撤销配置并验证拒绝。任务停留queued是预期，不当作生成成功。脚本只删除本次创建的测试库，不读取现有secret。

第九轮原基线：20项单测通过；真实PostgreSQL接受合同演练通过，证据 `/tmp/studio-managed-eval-4wkoppyl/result.json`，`supplier_calls=0`。正式模型、参考效果、账户权限与实际账单均未验证。
