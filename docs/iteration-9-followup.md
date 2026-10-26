# d633c295 后的候选余项核对

本次从 `d633c295a149a6e99379922885f913fbeb395d36` 的干净工作树继续，只本地实现和测试。没有推送、部署、真实凭据设置或供应商调用；USD3／CNY5独立额度均未使用。此记录补充第九轮快照，不把先前33组结果当作本次最后回归。

## 原8项的准确分类

这8项没有一项因“缺凭据”单独造成原协议拒绝，也没有一项已经核实退役。缺凭据、真实认证发送确认、精确报价及账户权限是所有候选共同的真实验收前置项。

| 原case／精确型号 | d633c295时的缺口 | 本次处理／剩余 |
| --- | --- | --- |
| 03 `gemini-3.8-flash` | 思考与输出token契约待核实 | 官方确认generateContent与总输出上限；精确型号补low思考。仍为64KiB／6000而非原2000／500，必须按实际界限重新报价。 |
| 06 `gpt-image-2.5-sunburst` | 图像参数待官方核实 | 确认现有3种尺寸、low／PNG／opaque、单图与参考编辑合法，补6种fixture；不是单纯换model。 |
| 08 `doubao-seedream-5-0-flash-260915` | 图像参数待官方核实 | 复核2K／Base64参考／单图禁组图，精确型号限定参数并纳入报价摘要。 |
| 09 `jimeng_seedream46_cvtob` | 公网参考桥接未验收，另发现managed runtime没有实际挂接 | 已补受治理桥接、操作员租户／配置／版本／目的地授权及撤销检查。公网S3与即梦取图仍待外部验收，候选工具继续拒绝执行。 |
| 10 `gemini-omni-1.1-flash` | 未核输出时长上界，不能给无界请求报价 | 官方模型卡限定原始输出3–10秒／720p，可按最多10秒报价，不承诺恰好10秒、不猜duration字段、不自动延长。 |
| 11 `MiniMax-H3-Lite` | 旧适配器5秒／768P与4秒／480P计划不符 | 精确型号改4秒／480P，规格纳入报价摘要；旧报价失效。 |
| 12 `doubao-seedance-2-0-mini-260615` | 旧720p与计划480p不符 | 精确型号改5秒／480p，规格纳入报价摘要；旧报价失效。 |
| 14 `gpt-transcribe` | 只有Whisper时间戳结果链，不能接受纯文本协议 | 新增显式transcription模式与文本结果、治理／恢复／双语界面／评测分支；不生成假时间戳、不允许应用为字幕、不自动换Whisper。 |

证据：[OpenAI／Gemini](candidate-openai-gemini-contracts.md)、[视频／Ark](candidate-video-ark-contracts.md)、[即梦受治理桥接](managed-jimeng-reference.md)、[纯文本转写](text-transcription.md)、候选计划 `eval/candidate-plan.json`。详细型号wire证据和最终验证结果随本次提交一并记录。

OpenAI Videos／Sora属于**另外排除的退役能力**，仍在本地拒绝；即梦视频仍没有核实可用官方接口，不伪造支持。Whisper旧时间戳协议与新的纯转写分开保留，弃用不等于已经关闭。

## 离线模型与S3

[具体离线引擎](local-model-engines.md)已实现Wav2Vec2模型调用与英文CTC算法、Wav2Lip固定上游推理调用、AudioGen本地checkpoint生成调用；测试使用合成emission／fake引擎、真实FFmpeg和进程网络限制。**三个模型都未运行真实权重、未验收模型质量**。重新检查确认PyTorch／Transformers／AudioCraft缺失。需要准备版本匹配的本地环境、完整审查且哈希固定的权重／源码／conditioning cache、用途许可和明确资源配置；不是缺云模型凭据。英文对齐不冒充中文，后两者非商用限制不因代码存在而解除，CPU性能仍未测。

本环境没有可用的合格S3服务。新正常源码安装动作 `go install github.com/minio/minio@RELEASE.2025-10-15T17-29-55Z` 已解析版本，但Go proxy重定向的 `storage.googleapis.com` ZIP下载返回 **Forbidden**；动作已停止，exit1，无二进制或服务生成。日志 `/tmp/studio-s3-minio-build.log`。没有改网络设置、重试旧被拒二进制或改用不合格Range／条件写语义。[S3证据](iteration-8-object-storage.md)记录完整边界。实际S3合同和媒体集成本次未运行；提供审核通过的本地兼容二进制／镜像或恢复正常官方依赖下载后，用loopback合成凭据即可验证，不必购买云资源。

## 迁移和运行变化

- 没有新增数据库schema迁移。新的纯文本转写任务不是旧二进制支持的结果类型；回滚须停服并恢复匹配版本完整备份，不能删字段假装兼容。
- 精确候选输出规格加入报价摘要；旧精确报价不可复用，须重新生成和审核可信报价。未知费用不自动释放或重发。
- 即梦桥接授权为操作员policy启动加载；API与worker须一致，更新需重启全部实例。没有默认授权、自动bucket配置或自动清理；签名URL失效不等于对象删除。
- 所有真实验收和正式配置依然未执行；本地协议通过不代表实际效果、账户资格、计费或生产容量已验收。

## 最后完整回归

本次产品与测试冻结在 `5fefb195fc2c51d1527d6243d37219245d42c1e1`，
32组实际执行全部通过，无产品测试失败；包括66项普通E2E、10项认证E2E、90个布局状态、31前端单测、842条双语覆盖及完整race／安全／恢复／实际媒体／多服务。最后结果见 [独立验证记录](iteration-9-followup-verification.md) 和
[机器可读报告](iteration-9-followup-test-report.json)。后续提交只收纳验证记录和文档。
Docker、真实S3、实际模型权重与供应商live分别记录为未运行，不能与本地通过混计。
