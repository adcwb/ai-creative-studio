# 数字人口播

项目导航新增“数字人口播”（`/projects/:id/avatar`），提供单人、固定机位、最长 30 秒的制作流程。

1. 上传照片／视频和录音，或复用本项目素材。照片宽高至少 256 像素；每个输入最大 10 MiB。
2. 登记形象名称、可追溯的授权依据，并确认已取得形象使用权、人工检查单人和面部清晰无遮挡。后端记录当前用户与时间。技术检查不代表人脸检测或授权真实性认证。
3. 上传录音，或输入文案通过现有 TTS 模型选择、报价和费用确认生成配音。TTS 是独立任务，不在口播任务中隐式追加付费请求；本流程不提供声音克隆。配音完成后点击“使用此配音”。
4. 选择形象和音频，填写声音使用依据并确认授权，再提交口播任务。音频必须不长于参考视频。
5. 成功后在页面预览带声音的视频，进入“素材与成片”，将素材加入时间轴。**勾选片段的“视频原音”**，或将原配音设为片段音轨，再添加字幕、背景、商品画面和音乐并导出。不会自动覆盖已有时间轴。

照片驱动与视频改口型分别报告能力。Eino 编排素材及授权检查、专用模型驱动、FFmpeg 合成及技术质检。模型输出的音轨被所选授权音频替换，成片烧录 `AI Generated` 标识，输出 H.264/AAC MP4，最大 1280×720、24fps、32 MiB。质检检查文件格式、时长与音轨；不自动认证人物一致性、口型或发音质量。

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

`compose.yaml` 与生产 Compose 的共享运行时透传上述变量。执行器及权重需放入自定义镜像或显式只读挂载；仓库基础镜像未包含它们。现有本地执行器限制仍适用：绝对可执行文件路径、干净环境、JSON stdin/stdout、120 秒超时及输出上限。执行器必须自行配置 Python/模型目录，不能依赖继承服务端凭据。

照片执行器协议：

```json
{"operation":"photo_avatar","model":"licensed-photo-model","reference":"<base64 PNG/JPEG>","reference_mime_type":"image/png","audio":"<base64 WAV/MP3>","audio_mime_type":"audio/wav"}
```

成功返回且仅返回 `{"video":"<base64 MP4>"}`。视频必须包含真实人物驱动结果，且不短于配音；日志写 stderr。视频路线保持现有 `lip_sync` 协议。基础成片合成依赖 FFmpeg 的 `libx264`、AAC、`drawtext` 及可用 sans 字体。

## 接口与存储

合同见 `api/openapi.json`：

- `GET /api/v1/projects/{id}/digital-humans`：当前项目形象，包含已撤销记录。
- `POST /api/v1/projects/{id}/digital-humans`：创建授权记录；相同请求键和内容重放返回原记录，不恢复已撤销授权。
- `POST /api/v1/projects/{id}/digital-humans/{avatarId}/revoke`：永久撤销，重复操作安全；恢复使用需新建授权记录。
- `POST /api/v1/projects/{id}/creative-jobs`：`kind=digital_human`，绑定 `avatar_id`、`audio_asset_id`、`voice_confirmed=true`、`voice_consent_reference`，以及匹配路由能力的 `expected_provider` / `expected_model` 和项目版本。文案说明 `prompt` 为 3–2000 字符。照片／视频路由由授权素材推导。
- `GET /api/v1/creative-media-capabilities`：`digital_human_photo`、`digital_human_video`；`ready` 仅说明执行配置与许可声明满足条件，**不是效果验收结果**。

沿用租户隔离、编辑者权限、CSRF、任务取消、持久化幂等和队列。结果未知时保留原请求内容和请求键重试；不要另建请求。执行前、模型调用后与素材发布前重新检查授权；撤销后不再发布在途结果，已有成片保留用于追溯，需另行处理。撤销不保证立即停止正在进行的模型计算。失败不自动重试。

文件模式授权记录在 `creative.json`；PostgreSQL 使用新增 `studio_digital_humans` 表，通过现有 `InitPostgres` 迁移机制创建。生产升级前使用现有迁移命令更新数据库，再更新 API/worker；运行用户权限和备份需覆盖新表。旧二进制不认识授权集合，不能与新版本混跑或直接回滚写入。

## 验收边界

回归测试用合成图像、音频及测试模型帧验证授权、幂等、隔离、撤销、两条路由和真实 FFmpeg 编码。没有调用商业模型或验证真实人物权重。上线前仍需经授权的真实短样片，人工检查人物一致性、中文口型、音画同步、生成耗时及单位成本。实时对话、3D 驱动、自动人脸检测、手势生成和多人物不在本次实现范围。
