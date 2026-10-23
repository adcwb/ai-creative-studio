# 第七轮创作协议增强与边界

2026-10-23 官方文档核验；所有远端适配仍未进行账号、质量、费用或可用性实测。本轮只运行合成本地媒体、假上游和明确标记的命令适配测试，不读取已有凭据、不配置账号、不执行外部模型。

| 需求 | 本轮实现 | 仍待验证/限制 |
| --- | --- | --- |
| 局部蒙版编辑 | `kind=image` 增加 `mask_asset_id`，必须同时引用同项目 `reference_asset_id`。OpenAI multipart `/images/edits` 发送 PNG mask | 仅 OpenAI 适配；PNG与参考图同尺寸、最多4MiB、须同时含全透明编辑区与全不透明保留区；模型可能修改其他区域，不能保证精确局部编辑 |
| Google 首帧/视频编辑 | `kind=video` 的项目参考素材为PNG/JPEG时走首帧；MP4时走视频编辑。原始 Interactions `user_input.content` 内联素材，结果继续用 Files ID 持久恢复 | 每份输入最多10MiB；配置的模型必须具备对应能力，代码不等于指定型号已验证；生成未获得持久Files ID仍可能结果/费用不明 |
| 即梦参考图 | 受控S3兼容对象桥接产生300秒预签名GET URL，通过官方 `image_urls` 提交；任务ID先落盘再轮询 | 桥接默认关闭；不创建公网/云资源、不设公开ACL。对象生命周期清理须部署时配置。进程重启后仅查询持久任务，不重复上传/生成 |
| 即梦视频 | 不接入 | 2026-10-23 查到官方3.0 Pro文档仍标“下线中”，未确认可用新接口，拒绝逆向消费网站或以旧接口冒充 |
| 角色跨镜头反馈 | `kind=consistency`，参考图与镜头图分别用 `reference_asset_id`、`comparison_asset_id`。服务端生成有界左右对照图送已配置视觉模型，任务保存输入资产ID和项目版本，返回差异/修正建议 | 默认视觉模型未配置，不能使用mock声称识别。仅比较发型、服装、配色与道具，不做人脸身份鉴定、相似度认证或锁脸保证 |
| 音乐生成 | `kind=music`，MiniMax原生器乐请求，内联hex MP3经实际媒体探测入库，可用于时间轴音乐轨 | 官方从2026-08-20不再向新用户开放该付费API；须 `MUSIC_EXISTING_ACCOUNT_ACK=true` 且账户确已具备权限。模型仅 `music-3.0` / `music-2.6`，不自动切换或试调用 |
| 音效生成 | `kind=sound_effect`，可替换本地模型命令适配，输出WAV经媒体探测入库 | 不内置或下载模型；默认禁用；只有适配协议合成测试，未验证实际音效质量 |
| 强制字幕对齐 | `mode=forced` 使用已保存镜头音频与必填剧本文字；可替换本地模型命令输出cue，校验时序、边界及拼接文本与原稿一致，人工应用后保存 | 不内置实际aligner；合成fixture不能证明词级对齐准确。任务完成不自动替换字幕，修改文字/时刻后必须改为manual |
| 口型同步 | `kind=lip_sync`，`reference_asset_id` 为项目MP4，`audio_asset_id`为项目WAV/MP3；可替换本地模型命令，生成新视频素材，不覆盖原稿 | 不内置或下载口型模型；仅校验执行协议与媒体安全，真实口型质量未测试 |
| OpenAI视频生命周期 | 正式配置和公开构造器的提交、查询、下载均在本地返回 `provider_retired`；保留历史数据 | 官方Videos API于2026-09-24关闭、无替代，不自动换供应商。包内旧假服务测试仅保留历史协议/恢复回归 |

## 官方依据

- [Google Omni生成、首帧、内联视频编辑与URI结果](https://ai.google.dev/gemini-api/docs/omni)：使用文档中的输入模态与 `FIRST_FRAME` / `VIDEO_0`控制；未从SDK便利字段推测REST响应。
- [OpenAI图片编辑](https://developers.openai.com/api/reference/resources/images/methods/edit)：mask透明区域用于编辑，不能承诺模型严格保持其他区域。
- [即梦4.6图像](https://docs.volcengine.com/docs/JimengAI/JimengAI-ImageGeneration46-InterfaceDocumentation?lang=zh)：`image_urls`、单图输出和原任务查询。
- [即梦3.0 Pro视频（下线中）](https://www.volcengine.com/docs/JimengAI/JimengAI-VideoGeneration30Pro-InterfaceDocumentation)：不据旧文档声明可用。
- [MiniMax音乐协议](https://platform.minimax.io/docs/api-reference/music-generation)及[账户开放限制](https://platform.minimax.io/docs/pricing/overview)：器乐、内联hex，以及存量账户限制。
- [OpenAI API弃用公告](https://developers.openai.com/api/docs/deprecations)：Videos API关闭。

## 安全配置与本地模型适配

音乐配置项为 `MUSIC_PROVIDER=minimax-music`、`MUSIC_MODEL`、`MUSIC_EXISTING_ACCOUNT_ACK`；运行时凭据仍使用服务端 `MINIMAX_API_KEY`。这份文件不包含凭据。填写配置不是live调用授权。

本地可替换模型使用：

| 操作 | 配置前缀 | JSON stdin | JSON stdout |
| --- | --- | --- | --- |
| 强制对齐 | `FORCED_ALIGNMENT_COMMAND` / `FORCED_ALIGNMENT_MODEL` | `operation=forced_alignment, model, audio`（base64）, `mime_type, duration, text` | `cues:[{start,end,text}]`（秒） |
| 口型同步 | `LIP_SYNC_COMMAND` / `LIP_SYNC_MODEL` | `operation=lip_sync, model, video`（base64）, `audio`（base64）, `audio_mime_type` | `video`（base64 MP4） |
| 音效生成 | `SOUND_EFFECT_COMMAND` / `SOUND_EFFECT_MODEL` | `operation=sound_effect, model, prompt` | `audio`（base64 WAV） |

命令须为管理员部署的绝对路径可执行普通文件；客户端不能传命令、路径或任意URL。通过参数数组直接执行，不使用shell拼接。每次在独立临时目录运行，只提供固定PATH/LANG和临时HOME，**不继承服务进程模型密钥或其他环境变量**。执行最长120秒，限制stdin/stdout尺寸，取消终止进程组；模型若需权重路径，由运维制作固定包装器。部署应额外使用容器CPU/内存/网络限制，该适配器本身不是恶意可执行程序沙箱。返回媒体仍经现有ffprobe/格式/时长限制，不能把任意base64响应当成功模型结果。

新增字段为可选，保留旧JSON记录与既有来源。新类型旧二进制不认识，回退必须先停服并恢复升级前完整数据目录，而非直接让旧二进制执行新任务。运行中或结果不明任务不自动重发；显式新建可能再次产生费用。

## 本地证据

`provider_enhancements_test.go` 覆盖mask尺寸/透明区与multipart、Google首帧/视频内联输入、Jimeng桥接默认关闭与提交、OpenAI退役拒绝、音乐字段/存量门槛、本地命令环境隔离/输出上限/文字不符拒绝、强制对齐完整创建→轮询→应用→篡改拒绝→重开、跨项目素材拒绝。定向 `go test -race` 已通过，最终全量结果以总验收记录为准。

这些测试不验证供应商权限、真实模型产物、人像一致性或费用。没有创建公网地址、远端对象或配置真实模型。
