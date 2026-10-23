# 第六轮补全阶段独立验收

承接第一阶段 `c64eefe`；旧阶段48项E2E和媒体证据保留于 [第一阶段记录](iteration-6-verification.md)，不冒充本阶段结果。本阶段最终应用与合同冻结于 `f17241b`（应用代码同 `6096077`），仅使用合成媒体、隔离临时DATA_DIR和本地假上游。没有真实模型凭据、付费调用、部署或推送。后续945a046仅添加视觉集成测试，7cdfadf仅添加字幕截图采集，应用代码未改变。

## 最终检查

| 项目 | 结果 |
| --- | --- |
| Go全包race / vet / gofmt / build | 全通过（含945a046新增视觉集成测试），race 28.027秒；134项顶层声明，专用provider harness默认跳过并由独立脚本执行 |
| 前端lint / typecheck / unit / build | 全通过；4文件15单测，52模块生产构建 |
| OpenAPI结构 / 实际HTTP契约 | 41个schema、38条path结构校验通过；434个实际HTTP响应通过（含轮询，不是434独立用例） |
| 原图像/剧本/分镜恢复smoke | 通过：真实SIGKILL重启、旧保存内容和幂等回执恢复、来源过期及故障保留 |
| 原媒体上传、合成、取消、恢复 | 通过：28个实际响应契约；真实MP4完整解码、音轨能量、字幕、取消与重启回执 |
| 新逐句对齐API与真实MP4 | 通过：10个实际响应契约；6.021秒MP4，H264 640×360 / AAC 48kHz双声道 / mov_text，逐句时间三种格式一致 |
| 完整桌面/手机E2E | 最终独立完整54/54通过（4.1分钟），桌面27+手机27；不将局部结果拼接为全量 |
| 90状态布局回归与provider联调 | 全通过：图像24、剧本30、分镜36状态；provider本地harness明确5次调用、零自动重放 |

最终原始日志使用 `/tmp/iteration6-followup-final-*`。交付副本保存到 `/workspace/ai-creative-studio-artifacts/iteration-6-followup/`，不将运行数据或生成媒体提交Git。

## 新增验收范围

**即梦持久任务恢复。** `TestJimeng*` 使用真实测试子进程终止/重启，并在假上游首次查询时检查磁盘已经保存taskID。覆盖原图像任务和项目创作任务、有ACK与无ACK四种场景；已记录ID只继续查询，submit保持1次；无ACK保持结果/费用不确定，绝不自动重新提交。覆盖取消后恢复、取消发生于ACK窗口、持久失败不查询、provider/model变化拒绝、跨空间、旧键重放及恢复回执容量限制。

**逐句字幕时间。** `scripts/alignment_check.py` 实际启动本地HTTP服务、生成合成图片和正弦音、保存人工cue并编码MP4，再从MP4字幕流解码SRT，核对与下载SRT/VTT一致：

- 第一镜第一句：0.250–0.750秒。
- 第一镜第二句：1.000–1.750秒。
- 第二镜句子：镜内0.500–1.500秒，合并后准确为2.500–3.500秒。

逐句cue存在时替代旧整镜字幕，不重复输出。验证重叠、越界和空文本拒绝且不增版；估算任务不自动修改时间轴；估算建议应用需要显式保存，修改过的cue不得继续伪称原始estimated/asr来源；跨空间、版本冲突、重复请求和真实重启回执均检查。配音从音频0开始，视频trim_start不偏移字幕时间。

**本地估算与真实ASR分离。** estimate按标点分句及文字权重分配时长，不识别音频、停顿或说话人；UI与返回warnings明确说明。真实ASR使用可替换接口和固定官方OpenAI `whisper-1` 时间戳协议，实际调用仍未验证。假上游测试覆盖multipart segment/word时间、缺失字段、非法/重叠/越界结果、重定向/超时/限流不重试、取消、真实running重启不重新POST、持久化故障不虚假显示运行中。真实模式未配置时返回503，不回退估算。

浏览器新增6项（桌面/手机各3项）：估算→应用→人工改0.25–0.75秒→响应丢失同键保存→真实导出VTT时间验证；旧版本任务不可应用；ASR请求只携带绑定音频/provider/model/version且不携带估算文本。最后一项仅拦截验证请求结构，不宣称识别音频。

**新增供应商适配。** Go测试覆盖Google Interactions图像/语音/视频、MiniMax图像/视频、独立豆包语音以及五协议视觉理解。视频的持久ID查询、原始REST响应解析、参考图能力边界、媒体大小/实际编码校验、签名与固定目标、去密钥安全下载、错误脱敏、无重定向或重复提交均以本地假上游验证。测试不证明任何真实账户权限、模型质量、人物一致性、费用或地区可用性。

## 审查发现与修复

- ASR UI曾错误复用estimate文本字段；后端按设计拒绝ASR预填文字。已修为仅estimate携带text，并新增两项桌面/手机请求结构回归。
- 新对齐数据加载过程中若先发布媒体state后等待任务列表，可能将初始空timeline误判为用户草稿；前端改为同批更新媒体、时间轴和编辑基线，未放宽断言。
- ASR按原音频实际duration验证，之后裁到镜头范围并加warnings；避免把视频trim当音频起点或将合法音频尾部误判为非法响应。
- Vision适配器对空白prompt、模型伪造provider/source_version、超时和401/403分类进行修正并补独立测试。
- 即梦无实现路径的vision能力登记误标已纠正为unsupported；旧视频API下线与参考图公网桥接需求分别标记，不能泛称全部能力可用。
- 新registry状态首次被严格OpenAPI enum拒绝，精确加入 `legacy_api_retiring` / `requires_public_reference_bridge` 后重跑合同通过，未放宽对象字段约束。
- 补全中期52项E2E在37项连续通过后主动中止（退出130），因为后端故障处理和ASR请求修复尚待合并；最终按冻结版完整重跑54项。

## 仍未验证或不支持

真实生成、ASR准确率、收费、供应商权限和角色一致性未验证；仅存在真实协议代码及本地故障/恢复证据。即梦旧视频API和依赖公网参考图桥接的编辑不冒充完成；没有部署公网素材桥接。音乐/音效仍为素材上传及混音，不是生成式音乐。无生产鉴权、多实例或企业计费系统；当前仍为本地顺序时间轴MVP。

最终54项首轮有53项通过，1项旧图像catalog测试读取Chrome响应body时报CDP `Network.getResponseBody: No data found for resource with given identifier`；保留 `e2e-first-run.log` 和trace，不按业务通过计数。trace确认首个templates请求收到200头后net::ERR_ABORTED，随后第二个请求200且完整1050字节，符合开发StrictMode初始化清理。其后独立完整复跑54/54通过，未修改业务断言。该初始化监听仍有已观察到的测试时序脆弱性，首次失败证据保留。

字幕截图专项2/2再次通过（桌面/手机），并人工查看 `iteration6-alignment-desktop-cues.png` / `iteration6-alignment-mobile-cues.png`：时间0.25–0.75秒、manual来源、估算非识别和未配置ASR说明清晰，手机控件纵向排布无裁切；完整页面截图同时保存。媒体播放截图为 `iteration6-media-desktop.png` / `iteration6-media-mobile.png`。

可分享演示文件：`alignment/sentence-cues.mp4`（合成图形与正弦音，非真实模型生成），配套 `.srt` / `.vtt` / `embedded.srt` / `ffprobe.json`；基础媒体回归文件位于 `media/`。最终日志前缀为 `iteration6-followup-final-`，最终完整E2E为 `iteration6-followup-final-e2e-rerun.log`。目录SHA-256清单见 `manifest.json`，保留父线程已有 `library-delivery.json`。无待批准或被拒绝的执行操作；上述外部效果与费用限制仍未验证。
