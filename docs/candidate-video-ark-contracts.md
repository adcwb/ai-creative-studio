# 视频与 Ark 图像候选复核（2026-10-26）

基线 `d633c295` 中 case08/10/11/12 的阻塞均为协议／规格复核，**不是厂商已退役，也不能仅归因于缺凭据**。本次仅本地合成协议验证，无供应商请求、真实凭据、购买或部署。

| 候选 | 本次可验证修复／结论 | 仍须外部验证 |
| --- | --- | --- |
| Ark `doubao-seedream-5-0-flash-260915`（08） | 保留原生 `/api/v3/images/generations`；精确型号显式 `2K`、`b64_json`、`png`、`sequential_image_generation=disabled`；单张原始图或单张 Base64 参考图，不请求组图 | 开通权限、真实尺寸／效果、价格与费用证据 |
| Google `gemini-omni-1.1-flash`（10） | 原生 Interactions 文生视频已有实现。官方型号卡明确原始输出 3–10 秒；单次原始文本请求按 **最多 10 秒、720p** 准备报价，不能写成保证恰好 10 秒 | 账号、真实质量、供应商计费与输入费用；本轮小样不覆盖编辑、延长、多轮 |
| MiniMax `MiniMax-H3-Lite`（11） | 精确型号从固定 5 秒／768P 改成 **4 秒／480P**，适用于原始文本和首帧；其他型号保持原契约 | Pay-as-you-go 权限、真实媒体和费用 |
| Ark `doubao-seedance-2-0-mini-260615`（12） | 精确型号改成 **5 秒／480p**，文本和首帧均覆盖；其他型号仍为原规格 | 账号开通、实际输出、费用与质量 |

## 官方依据与证据边界

- [MiniMax V2 创建接口](https://platform.minimax.io/docs/api-reference/video-generation-v2-create)：本次直接读取正文，Lite 支持 480P／768P、4–15 秒；有原始文本与首帧输入模式。没有把其他 H3 变体自动映射为 Lite。
- [Ark 视频创建接口](https://www.volcengine.com/docs/ark/create-video-generation-task-api)：本次官方搜索索引确认 Seedance 2.0 mini 可用 480p／720p、2.0 系列时长范围 4–15 秒。[2.0 教程](https://www.volcengine.com/docs/ark/seedance-2-0)列出精确 mini 型号。
- [Ark 图像接口](https://www.volcengine.com/docs/ark/image-generation-api)的官方索引明确 Base64 参考、`b64_json` 返回及 `disabled` 单图；[Seedream 5.0 教程](https://www.volcengine.com/docs/ark/seedream-5-0-pro)列出精确 Flash 型号与 2K／PNG 示例。直接网页为 JavaScript 页面；正常 `curl --fail --location https://www.volcengine.com/docs/ark/create-video-generation-task-api` 返回 HTTP 403，已停止该下载操作，没有更换网络或认证绕过。此处是可读官方索引证据，**不是完整下载 API schema 或 live 验收**。
- [Google 型号卡](https://ai.google.dev/gemini-api/docs/models/gemini-omni-flash)（页面更新 2026-10-22）直接列出稳定 `gemini-omni-1.1-flash` 输出 3–10 秒和已关闭旧 preview。[Omni 指南](https://ai.google.dev/gemini-api/docs/omni)确认现有 URI／720p／单次同步请求；[Interactions schema](https://ai.google.dev/api/interactions-api-v1)虽列出 `duration:string`，未给出本次足以确认的单位／格式示例，代码没有猜测 `10s` 参数。上界来自稳定型号卡，不能从提示词“十秒”推断。

## 报价、迁移与测试

输出规格通过共享 helper 同时进入适配器和服务端 `ExecutionRequest.Parameters`（Google 最大时长仅用于无参考原始 `video`）。Ark 图像的普通图片任务与项目创作任务都加入同一规格；其他型号保留旧参数摘要。**旧的精确报价摘要不能用于新规格**，需要重新取得有来源、有效期、原币种的可信上限；没有填入旧市场估价或生成假报价。USD 3 与 CNY 5 独立总上限不变；尚未执行的旧候选任务如遇摘要不匹配应停下重评，不自动重提／退还未知费用。

`provider_video_candidates_test.go` 覆盖精确型号与相似型号不串用、文本／首帧请求规格、旧报价失效与规格改变拒绝；`provider_image_candidates_test.go` 覆盖原图／参考单图字段、普通及项目图片报价规格。已有视频生命周期测试改用精确候选验证查询、下载、取消和 Google 新实例恢复不重发。所有媒体响应均为本地 fixture，不证明模型质量或真实账单。

候选计划可解除这四项原 wire blocker，但共同的认证传输、配置授权、内容审核、可信报价和 live 开始确认仍不可省略。OpenAI Videos/Sora 退役拒绝与即梦未核实视频拒绝不变。
