# 候选协议复核（2026-10-26）

仅本地合成上游验证；未发送真实认证或模型请求。型号出现在官方文档不证明账号权限、价格结算或生成质量。

## case-03：Gemini 3.8 Flash

[官方 generateContent 指南](https://ai.google.dev/gemini-api/docs/generate-content/whats-new-gemini-3.8)仍给出该模型的原生请求。[该协议思考指南](https://ai.google.dev/gemini-api/docs/generate-content/thinking)说明输出限额包括思考，并支持 `thinkingConfig.thinkingLevel=low`。现有 `gemini-text` 路径可用，不需要静默切换 Interactions。

适配器对精确型号 `gemini-3.8-flash` 增加 low 思考、关闭思考摘要；其他型号不接收未经复核的新增字段。仍是单候选、JSON、64 KiB 请求体上限和 6000 总输出 token 上限，**不是原先 2000 输入／500 输出的小样规格**。报价必须覆盖实际请求上限；缺报价时拒绝。只接收 STOP；MAX_TOKENS、CONTINUATION 和安全拒绝不生成成功结果、不续传、不重试。思考内容不混入创作 JSON。

## case-06：OpenAI Sunburst

[官方图像指南](https://developers.openai.com/api/docs/guides/image-generation)确认 `gpt-image-2.5-sunburst` 支持 Images generations/edits，以及 low、PNG、opaque、自定义尺寸。当前 1024×1024、1536×864、1024×1280 均满足边长为 16 倍数、边长不超过 3840、宽高比 1:3..3:1、总像素 655360..8294400 的条件。因此此前“待官方复核”可以解除，无需编造新协议或改变图片规格。

新测试对三个画幅分别验证生成与参考编辑的精确型号、数量 1、尺寸、质量、格式、背景和内联参考字段，解码合成 PNG 检查尺寸。没有使用 auto 尺寸、流式预览或远端结果 URL。需要新的可信报价，不能把示例输出价格当含参考输入的最终费用。

## case-14：OpenAI gpt-transcribe

[官方文件转写指南](https://developers.openai.com/api/docs/guides/speech-to-text)给出 `gpt-transcribe` 的 multipart 转写和 JSON 文本／语言响应。它不等于现有 Whisper 的 `verbose_json` 时间戳协议。基线缺口是应用缺少显式纯文本转写结果链路，不能靠换模型或伪造字幕时间解决；本次已补[独立纯文本模式](text-transcription.md)，不生成时间戳。原阻塞不属于模型退役，也不只是缺凭据。原 Whisper 时间戳接口已标弃用，官方计划 2027-02-26 关闭，后续应迁移到本地／独立对齐。

## 本地证据

`backend/internal/application/provider_candidate_contract_test.go`：Gemini STOP/限额截断/续传/安全拒绝四种响应；Sunburst 三种画幅×生成／参考六种请求。使用 `httptest`、合成密钥和 PNG；没有真实模型质量结论。
