# 数字人口播

项目导航新增“数字人口播”（`/projects/:id/avatar`），提供单人、固定机位的制作流程。

1. 上传照片／视频和录音，或复用本项目素材。照片宽高至少 256 像素；不设项目素材大小或时长额度。
2. 登记形象名称、可追溯的授权依据，并确认已取得形象使用权、人工检查单人和面部清晰无遮挡。后端记录当前用户与时间。技术检查不代表人脸检测或授权真实性认证。
3. 上传录音，或输入文案通过现有 TTS 模型选择与明确提交生成配音。TTS 是独立任务，不在口播任务中隐式追加付费请求；本流程不提供声音克隆。配音完成后点击“使用此配音”。
4. 选择形象和音频，填写声音使用依据并确认授权，再提交口播任务。音频必须不长于参考视频。
5. 成功后在页面预览带声音的视频，进入“素材与成片”，将素材加入时间轴。**勾选片段的“视频原音”**，或将原配音设为片段音轨，再添加字幕、背景、商品画面和音乐并导出。不会自动覆盖已有时间轴。

照片驱动与视频改口型分别报告能力。Eino 编排素材及授权检查、专用模型驱动、FFmpeg 合成及技术质检。模型输出的音轨被所选授权音频替换，成片烧录 `AI Generated` 标识，输出 H.264/AAC MP4，保留模型原生尺寸与帧节奏（H.264要求偶数像素），不人为压到720p/24fps或32MiB；这不代表原生4K生成。质检检查文件格式、时长与音轨；不自动认证人物一致性、口型或发音质量。

## 引擎配置

默认禁用，不附带模型权重，不生成假数字人结果。部署方需要安装具备适当许可的离线模型执行器，并在 API 和所有 worker 上配置相同路径及模型：

```dotenv
DIGITAL_HUMAN_LICENSE_ACK=false
DIGITAL_HUMAN_PHOTO_COMMAND=/opt/studio/avatar/photo-runner
DIGITAL_HUMAN_PHOTO_MODEL=licensed-photo-model
LIP_SYNC_COMMAND=/opt/studio/avatar/video-runner
LIP_SYNC_MODEL=licensed-lip-sync-model
```

审核引擎、权重及依赖的使用范围后，才将 `DIGITAL_HUMAN_LICENSE_ACK` 设为 `true`。这个开关是部署方声明，不会授予任何模型许可。现有 Wav2Lip 适配基础不代表商业使用许可或真实效果验收；本次不安装／启用 Wav2Lip 权重。商业平台不能仅靠设置开关绕过上游限制。

`compose.yaml` 与生产 Compose 的共享运行时透传这些变量。执行器及权重需放入自定义镜像或显式只读挂载；基础镜像不附带它们。可信本地进程不等于系统沙箱，执行器必须由部署方安装维护，不能接受浏览器命令或依赖继承服务端凭据。

### 推荐：文件协议

```dotenv
DIGITAL_HUMAN_PHOTO_PROTOCOL=files-v1
DIGITAL_HUMAN_PHOTO_TIMEOUT_SECONDS=120
LIP_SYNC_PROTOCOL=files-v1
LIP_SYNC_TIMEOUT_SECONDS=120
```

成功配置仅证明路径/模型/协议可用，不证明模型权重、效果或许可已验收。超时是运行保护，可按引擎调整；既有 worker 另有执行截止时间。超时/进程中断不自动重发，也没有分段推理续跑。

执行器从 JSON stdin 接收（路径由后端生成，不由用户指定）：

```json
{"protocol":"studio-avatar-files-v1","operation":"photo_avatar","model":"licensed-photo-model","reference_path":"<private absolute path>","reference_mime_type":"image/png","audio_path":"<private absolute path>","audio_mime_type":"audio/wav","output_path":"<private workdir>/output.mp4"}
```

视频改口型使用 `operation=lip_sync`、`reference_mime_type=video/mp4`，其余合同一致。执行器读取私有输入副本，在 output_path 写完整真实 MP4，成功后 stdout **仅**返回 `{"protocol":"studio-avatar-files-v1","video_path":"output.mp4"}`。控制响应最大8KiB，与视频大小无关；日志写stderr（服务端丢弃，不能依赖其诊断敏感信息）。服务端拒绝其他路径、软链接、非普通文件和解码失败，冻结输出副本后全量解码验证；随后用授权音频合成、技术质检并流式入库。路径含空格和 Windows 路径按普通 JSON 字符串编码，禁止自行拼接 shell 命令。

文件协议不设输入10MiB、时长30秒、输出32MiB门槛；真实单请求窗口、GPU/内存能力由具体执行器决定并须明确报告，不可静默截断或返回假结果。实际模型失败不会回退幻灯片、原视频循环或合成嘴型。

### 兼容：旧 JSON 协议

未设置 PROTOCOL 时仍为 `json-v1`，避免升级后静默破坏现有执行器。其旧版内存 IPC 边界为单项输入10MiB、输出32MiB：不是全局上传配额。输入超过边界返回 `avatar_legacy_protocol_limit`，需部署方切换文件协议；项目不会替用户截断。

```json
{"operation":"photo_avatar","model":"licensed-photo-model","reference":"<base64 PNG/JPEG>","reference_mime_type":"image/png","audio":"<base64 WAV/MP3>","audio_mime_type":"audio/wav"}
```

成功返回且仅返回 `{"video":"<base64 MP4>"}`。视频路线沿用 `lip_sync` 的 video/audio base64 字段。两种协议的模型视频都不得短于配音，合成依赖 FFmpeg 的 libx264、AAC、drawtext 和可用 sans 字体。

## 接口与存储

合同见 `api/openapi.json`：

- `GET /api/v1/projects/{id}/digital-humans`：当前项目形象，包含已撤销记录。
- `POST /api/v1/projects/{id}/digital-humans`：创建授权记录；相同请求键和内容重放返回原记录，不恢复已撤销授权。
- `POST /api/v1/projects/{id}/digital-humans/{avatarId}/revoke`：永久撤销，重复操作安全；恢复使用需新建授权记录。
- `POST /api/v1/projects/{id}/creative-jobs`：`kind=digital_human`，绑定 `avatar_id`、`audio_asset_id`、`voice_confirmed=true`、`voice_consent_reference`，以及匹配路由能力的 `expected_provider` / `expected_model` 和项目版本。文案说明 `prompt` 为 3–2000 字符。照片／视频路由由授权素材推导。
- `GET /api/v1/creative-media-capabilities`：`digital_human_photo`、`digital_human_video`；`ready` 仅说明执行配置与许可声明满足条件，**不是效果验收结果**。

沿用租户隔离、编辑者权限、CSRF、任务取消、持久化幂等和队列。结果未知时保留原请求内容和请求键重试；不要另建请求。执行前、实际进程派发前、模型调用后与素材发布前重新检查形象授权；安全运行模式还核对声音确认者当前成员权限；撤销后不再发布在途结果，已有成片保留用于追溯，需另行处理。撤销不保证立即停止正在进行的模型计算。失败不自动重试。

文件模式授权记录在 `creative.json`；PostgreSQL 使用新增 `studio_digital_humans` 表，通过现有 `InitPostgres` 迁移机制创建。生产升级前使用现有迁移命令更新数据库，再更新 API/worker；运行用户权限和备份需覆盖新表。旧二进制不认识授权集合，不能与新版本混跑或直接回滚写入。

## 验收边界

回归测试用合成图像、音频及测试模型帧验证授权、幂等、隔离、撤销、两条路由和真实 FFmpeg 编码。没有调用商业模型或验证真实人物权重。上线前仍需经授权的真实短样片，人工检查人物一致性、中文口型、音画同步、生成耗时及单位成本。实时对话、3D 驱动、自动人脸检测、手势生成和多人物不在本次实现范围。

部署管理员也可在“系统设置 → 后端配置 → 模型与本地服务”保存 DIGITAL_HUMAN_PHOTO_COMMAND/MODEL/PROTOCOL/TIMEOUT_SECONDS、LIP_SYNC_PROTOCOL/TIMEOUT_SECONDS 与 DIGITAL_HUMAN_LICENSE_ACK（界面分组名称以当前语言显示为准）。只接受已实现的 json-v1/files-v1 协议及正整数截止时间，保存不改变正在运行的执行器；协调重启所有 API/worker 后生效。命令仍须指向部署方信任、实际安装的绝对可执行文件，普通团队管理员和创作者不能设置命令，勾选许可声明不代表已取得商业许可。
